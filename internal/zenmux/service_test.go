package zenmux

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	_ "cpa-usage-keeper/internal/timeutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAPIKeyPreviewMasksKeys(t *testing.T) {
	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "", want: "****"},
		{key: "sk-m-abcdefgh", want: "sk-m-****efgh"},
		{key: "  sk-m-abcdefgh  ", want: "sk-m-****efgh"},
		{key: "sk-m-1234567890abcdef", want: "sk-m-****cdef"},
	} {
		if got := APIKeyPreview(test.key); got != test.want {
			t.Fatalf("APIKeyPreview(%q) = %q, want %q", test.key, got, test.want)
		}
	}
}

func TestParseBalanceResponseToleratesShapes(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantTotal float64
		wantTopUp float64
		wantBonus float64
	}{
		{name: "top-level snake", body: `{"total_balance":12.34,"top_up_credits":10,"bonus_credits":2.34}`, wantTotal: 12.34, wantTopUp: 10, wantBonus: 2.34},
		{name: "top-level camel", body: `{"totalBalance":5.5,"topUpCredits":3,"bonusCredits":2.5}`, wantTotal: 5.5, wantTopUp: 3, wantBonus: 2.5},
		{name: "data nested", body: `{"data":{"total_balance":42,"top_up_credits":30,"bonus_credits":12}}`, wantTotal: 42, wantTopUp: 30, wantBonus: 12},
		{name: "balance alias", body: `{"balance":7.25,"topup_credits":7,"bonusCredits":0.25}`, wantTotal: 7.25, wantTopUp: 7, wantBonus: 0.25},
		{name: "numeric strings", body: `{"total_balance":"12.5","top_up_credits":"10","bonus_credits":"2.5"}`, wantTotal: 12.5, wantTopUp: 10, wantBonus: 2.5},
		{name: "missing topup bonus defaults zero", body: `{"total_balance":8}`, wantTotal: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := parseBalanceResponse([]byte(test.body))
			if err != nil {
				t.Fatalf("parseBalanceResponse returned error: %v", err)
			}
			if result.TotalBalance != test.wantTotal || result.TopUpCredits != test.wantTopUp || result.BonusCredits != test.wantBonus {
				t.Fatalf("unexpected parsed result: %+v", result)
			}
		})
	}

	for _, body := range []string{
		``,
		`{"status":"ok"}`,
		`{"data":{"status":"ok"}}`,
		`not-json`,
	} {
		if _, err := parseBalanceResponse([]byte(body)); err == nil {
			t.Fatalf("expected parse error for body %q", body)
		}
	}
}

func TestVerifyBalanceSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-secret-key-1234" {
			t.Fatalf("expected Bearer authorization header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"totalBalance":100.5,"topUpCredits":80,"bonusCredits":20.5}}`))
	}))
	defer server.Close()

	result, err := verifyBalance(context.Background(), &http.Client{Timeout: time.Second}, server.URL, "sk-secret-key-1234")
	if err != nil {
		t.Fatalf("verifyBalance returned error: %v", err)
	}
	if result.TotalBalance != 100.5 || result.TopUpCredits != 80 || result.BonusCredits != 20.5 {
		t.Fatalf("unexpected balance result: %+v", result)
	}
}

func TestVerifyBalanceMapsHTTPErrorWithTruncatedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid or missing api key for management endpoint"))
	}))
	defer server.Close()

	_, err := verifyBalance(context.Background(), &http.Client{Timeout: time.Second}, server.URL, "sk-secret-key-1234")
	if err == nil {
		t.Fatal("expected verifyBalance error")
	}
	want := "HTTP 401: invalid or missing api key for management endpoint"
	if err.Error() != want {
		t.Fatalf("expected error %q, got %q", want, err.Error())
	}
	if strings.Contains(err.Error(), "sk-secret-key-1234") {
		t.Fatalf("error must not contain api key: %s", err.Error())
	}
}

func TestVerifyBalanceTruncatesLongErrorBody(t *testing.T) {
	body := strings.Repeat("x", 500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	_, err := verifyBalance(context.Background(), &http.Client{Timeout: time.Second}, server.URL, "sk-secret-key-1234")
	if err == nil {
		t.Fatal("expected verifyBalance error")
	}
	if !strings.HasPrefix(err.Error(), "HTTP 502: ") || len(err.Error()) != len("HTTP 502: ")+maxVerifyErrorBodyExcerpt {
		t.Fatalf("expected truncated error, got %q", err.Error())
	}
}

func TestVerifyBalanceFailsOnUnknownShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":"ok but no balance"}`))
	}))
	defer server.Close()

	_, err := verifyBalance(context.Background(), &http.Client{Timeout: time.Second}, server.URL, "sk-secret-key-1234")
	if err == nil || !strings.Contains(err.Error(), "total balance") {
		t.Fatalf("expected descriptive parse error, got %v", err)
	}
}

func TestVerifyBalanceTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	start := time.Now()
	_, err := verifyBalance(context.Background(), &http.Client{Timeout: 50 * time.Millisecond}, server.URL, "sk-secret-key-1234")
	if err == nil {
		t.Fatal("expected verifyBalance timeout error")
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Fatalf("expected timeout to abort request promptly, took %v", time.Since(start))
	}
	if strings.Contains(err.Error(), "sk-secret-key-1234") {
		t.Fatalf("error must not contain api key: %s", err.Error())
	}
}

func TestServiceCreateValidatesAndDefaults(t *testing.T) {
	db := openZenMuxTestDB(t)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	if _, err := service.Create(context.Background(), CreateRequest{Name: "  ", APIKey: "sk-123456"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for empty name, got %v", err)
	}
	if _, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "  "}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for empty api key, got %v", err)
	}
	for _, endpoint := range []string{"ftp://example.com", "not-a-url", "example.com/path"} {
		if _, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", Endpoint: endpoint}); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected validation error for endpoint %q, got %v", endpoint, err)
		}
	}

	row, err := service.Create(context.Background(), CreateRequest{Name: "  主账号  ", APIKey: "sk-123456", AuthIndex: stringPtr("auth-1")})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if row.Name != "主账号" || row.APIKey != "sk-123456" {
		t.Fatalf("unexpected created row: %+v", row)
	}
	if row.Endpoint != entities.DefaultZenMuxEndpoint {
		t.Fatalf("expected default endpoint, got %q", row.Endpoint)
	}
	if row.AuthIndex == nil || *row.AuthIndex != "auth-1" {
		t.Fatalf("expected auth index to persist, got %+v", row.AuthIndex)
	}
	if row.CheckStatus != "" || row.CheckError != "" || row.CheckedAt != nil {
		t.Fatalf("expected fresh row to be never-checked: %+v", row)
	}
	if APIKeyPreview(row.APIKey) != "****" {
		t.Fatalf("unexpected preview %q", APIKeyPreview(row.APIKey))
	}
}

func TestServiceUpdateSemantics(t *testing.T) {
	db := openZenMuxTestDB(t)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", Endpoint: "https://custom.example.com/balance", AuthIndex: stringPtr("auth-1")})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// 空 api_key 表示不修改；name 更新生效；auth_index 显式 null 解除绑定。
	updated, err := service.Update(context.Background(), row.ID, UpdateRequest{
		Name:         stringPtr("新名字"),
		APIKey:       stringPtr("  "),
		AuthIndexSet: true,
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Name != "新名字" || updated.APIKey != "sk-123456" || updated.Endpoint != "https://custom.example.com/balance" {
		t.Fatalf("unexpected updated row: %+v", updated)
	}
	if updated.AuthIndex != nil {
		t.Fatalf("expected auth index to be unbound, got %+v", updated.AuthIndex)
	}

	// 重新绑定：auth_index 提供时设置。
	rebound, err := service.Update(context.Background(), row.ID, UpdateRequest{AuthIndex: stringPtr("auth-2"), AuthIndexSet: true})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if rebound.AuthIndex == nil || *rebound.AuthIndex != "auth-2" {
		t.Fatalf("expected auth index auth-2, got %+v", rebound.AuthIndex)
	}

	// 未提供 auth_index 时保持原值；endpoint 空串回退默认。
	kept, err := service.Update(context.Background(), row.ID, UpdateRequest{Endpoint: stringPtr("")})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if kept.AuthIndex == nil || *kept.AuthIndex != "auth-2" {
		t.Fatalf("expected auth index preserved, got %+v", kept.AuthIndex)
	}
	if kept.Endpoint != entities.DefaultZenMuxEndpoint {
		t.Fatalf("expected default endpoint, got %q", kept.Endpoint)
	}

	if _, err := service.Update(context.Background(), row.ID, UpdateRequest{Name: stringPtr(" ")}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for blank name, got %v", err)
	}
	if _, err := service.Update(context.Background(), row.ID, UpdateRequest{Endpoint: stringPtr("ftp://x")}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for bad endpoint, got %v", err)
	}
	if _, err := service.Update(context.Background(), 9999, UpdateRequest{Name: stringPtr("x")}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected record not found, got %v", err)
	}
}

func TestServiceDeleteRemovesRow(t *testing.T) {
	db := openZenMuxTestDB(t)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := service.Delete(context.Background(), row.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if err := service.Delete(context.Background(), row.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected record not found on second delete, got %v", err)
	}
	rows, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty list after delete, got %+v", rows)
	}
}

func TestServiceVerifyPersistsResult(t *testing.T) {
	db := openZenMuxTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"total_balance":66.6,"top_up_credits":60,"bonus_credits":6.6}`))
	}))
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	verified, err := service.Verify(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified.CheckStatus != entities.ZenMuxCredentialCheckStatusSuccess {
		t.Fatalf("expected success status, got %q", verified.CheckStatus)
	}
	if verified.CheckedAt == nil || verified.TotalBalance == nil || *verified.TotalBalance != 66.6 || verified.TopUpCredits == nil || *verified.TopUpCredits != 60 || verified.BonusCredits == nil || *verified.BonusCredits != 6.6 || verified.CheckError != "" {
		t.Fatalf("unexpected verified row: %+v", verified)
	}

	// 失败验证清空余额并写入错误。
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer failing.Close()
	row2, err := service.Create(context.Background(), CreateRequest{Name: "坏凭证", APIKey: "sk-bad-key-123", Endpoint: failing.URL})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	failed, err := service.Verify(context.Background(), row2.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if failed.CheckStatus != entities.ZenMuxCredentialCheckStatusFailed || failed.TotalBalance != nil || failed.TopUpCredits != nil || failed.BonusCredits != nil {
		t.Fatalf("expected failed check with nil balances: %+v", failed)
	}
	if failed.CheckError != "HTTP 403: forbidden" {
		t.Fatalf("unexpected check error %q", failed.CheckError)
	}
	if strings.Contains(failed.CheckError, "sk-bad-key-123") {
		t.Fatalf("check error must not contain api key: %s", failed.CheckError)
	}
}

func TestServiceVerifyPersistsParsingFailure(t *testing.T) {
	db := openZenMuxTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	verified, err := service.Verify(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified.CheckStatus != entities.ZenMuxCredentialCheckStatusFailed || !strings.Contains(verified.CheckError, "total balance") {
		t.Fatalf("expected descriptive parsing failure, got %+v", verified)
	}
}

func TestServiceStatsByAuthIndexes(t *testing.T) {
	db := openZenMuxTestDB(t)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	if err := db.Create(&entities.UsageIdentity{
		Name:            "Auth One",
		AuthType:        entities.UsageIdentityAuthTypeAuthFile,
		AuthTypeName:    "oauth",
		Identity:        "auth-1",
		Type:            "claude",
		TotalRequests:   100,
		SuccessCount:    98,
		FailureCount:    2,
		InputTokens:     125_000,
		CacheReadTokens: 30_000,
		TotalTokens:     123_456,
	}).Error; err != nil {
		t.Fatalf("seed usage identity: %v", err)
	}
	// 已删除身份不参与统计。
	if err := db.Create(&entities.UsageIdentity{
		Name:          "Deleted Auth",
		AuthType:      entities.UsageIdentityAuthTypeAuthFile,
		AuthTypeName:  "oauth",
		Identity:      "auth-deleted",
		Type:          "claude",
		TotalRequests: 10,
		SuccessCount:  9,
		IsDeleted:     true,
	}).Error; err != nil {
		t.Fatalf("seed deleted usage identity: %v", err)
	}
	// AI Provider 类型不参与 Auth File 统计。
	if err := db.Create(&entities.UsageIdentity{
		Name:          "Provider Key",
		AuthType:      entities.UsageIdentityAuthTypeAIProvider,
		AuthTypeName:  "apikey",
		Identity:      "auth-1",
		Type:          "openai",
		TotalRequests: 999,
		SuccessCount:  999,
	}).Error; err != nil {
		t.Fatalf("seed provider identity: %v", err)
	}

	stats, err := service.StatsByAuthIndexes(context.Background(), []string{"auth-1", "auth-missing", "", "auth-1", "auth-deleted"})
	if err != nil {
		t.Fatalf("StatsByAuthIndexes returned error: %v", err)
	}
	item, ok := stats["auth-1"]
	if !ok {
		t.Fatalf("expected stats for auth-1, got %+v", stats)
	}
	if item.TotalRequests != 100 || item.SuccessCount != 98 || item.FailureCount != 2 || item.TotalTokens != 123456 || item.CacheReadTokens != 30000 {
		t.Fatalf("unexpected stats: %+v", item)
	}
	if math.Abs(item.SuccessRate-0.98) > 1e-9 {
		t.Fatalf("expected success rate 0.98, got %v", item.SuccessRate)
	}
	if math.Abs(item.CacheReadRate-0.24) > 1e-9 {
		t.Fatalf("expected cache read rate 0.24, got %v", item.CacheReadRate)
	}
	if _, ok := stats["auth-missing"]; ok {
		t.Fatalf("expected missing auth index to be absent, got %+v", stats)
	}
	if _, ok := stats["auth-deleted"]; ok {
		t.Fatalf("expected deleted identity to be absent, got %+v", stats)
	}
}

func TestServiceStatsZeroSuccessRateForNoRequests(t *testing.T) {
	db := openZenMuxTestDB(t)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})
	if err := db.Create(&entities.UsageIdentity{
		Name:         "Empty Auth",
		AuthType:     entities.UsageIdentityAuthTypeAuthFile,
		AuthTypeName: "oauth",
		Identity:     "auth-empty",
		Type:         "claude",
	}).Error; err != nil {
		t.Fatalf("seed usage identity: %v", err)
	}
	stats, err := service.StatsByAuthIndexes(context.Background(), []string{"auth-empty"})
	if err != nil {
		t.Fatalf("StatsByAuthIndexes returned error: %v", err)
	}
	item := stats["auth-empty"]
	if item.SuccessRate != 0 || item.CacheReadRate != 0 {
		t.Fatalf("expected zero rates for empty identity, got %+v", item)
	}
}

func openZenMuxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "zenmux.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	// Windows 下 TempDir 清理要求文件句柄先关闭，否则 RemoveAll 会因文件占用失败。
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&entities.ZenMuxCredential{}, &entities.UsageIdentity{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return db
}

func stringPtr(value string) *string {
	return &value
}
