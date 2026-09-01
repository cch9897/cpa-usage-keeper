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

// 注意：环境变量代理测试必须排在任何发起真实 HTTP 请求的测试之前，且一个进程只能测一组代理环境。
// http.ProxyFromEnvironment 在进程内首次调用时会缓存环境变量，后续 t.Setenv 无法覆盖；
// 本文件第一个发起请求的测试（plain client）会把真实环境（如 HTTP_PROXY=192.168.50.10:7890）写入缓存。
func TestVerifyFallsBackToEnvironmentProxy(t *testing.T) {
	db := openZenMuxTestDB(t)
	var proxySawRequest bool
	var proxySawAuth string
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxySawRequest = true
		proxySawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"total_balance":30}`))
	}))
	defer proxyServer.Close()
	t.Setenv("HTTP_PROXY", proxyServer.URL)
	t.Setenv("NO_PROXY", "")

	// 传输层 wiring：空 proxy_url 必须落到 ProxyFromEnvironment（即当前环境代理）。
	transport, err := buildVerifyTransport("")
	if err != nil {
		t.Fatalf("buildVerifyTransport returned error: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://zenmux.example.com/balance", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		t.Fatalf("proxy function returned error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != proxyServer.URL {
		t.Fatalf("expected environment proxy %s, got %+v", proxyServer.URL, proxyURL)
	}

	// 行为：service.Verify 经由环境代理成功；.invalid 目标永不解析且非 loopback，
	// 不会触发 Go 的本地代理旁路，只有真正走代理才能拿到余额。
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})
	row, err := service.Create(context.Background(), CreateRequest{
		Name:     "环境代理凭证",
		APIKey:   "sk-env-proxy-key-123",
		Endpoint: "http://zenmux.invalid/balance",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	verified, err := service.Verify(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !proxySawRequest {
		t.Fatal("expected request to fall back to environment proxy")
	}
	if proxySawAuth != "Bearer sk-env-proxy-key-123" {
		t.Fatalf("expected proxy to receive Bearer header, got %q", proxySawAuth)
	}
	if verified.CheckStatus != entities.ZenMuxCredentialCheckStatusSuccess || verified.TotalBalance == nil || *verified.TotalBalance != 30 {
		t.Fatalf("unexpected verified row: %+v", verified)
	}
}

func TestBuildVerifyTransportUsesExplicitProxy(t *testing.T) {
	transport, err := buildVerifyTransport("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("buildVerifyTransport returned error: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://zenmux.example.com/balance", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		t.Fatalf("proxy function returned error: %v", err)
	}
	if proxyURL == nil || proxyURL.Host != "127.0.0.1:7890" {
		t.Fatalf("expected explicit proxy 127.0.0.1:7890, got %+v", proxyURL)
	}
}

func TestAPIKeyPreviewMasksKeys(t *testing.T) {
	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "", want: "****"},
		{key: "short", want: "****"},
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
		{name: "official total_credits shape", body: `{"success":true,"data":{"currency":"usd","total_credits":482.74,"top_up_credits":35.0,"bonus_credits":447.74}}`, wantTotal: 482.74, wantTopUp: 35, wantBonus: 447.74},
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

func TestServiceVerifyRoutesThroughExplicitProxy(t *testing.T) {
	db := openZenMuxTestDB(t)
	var proxySawRequest bool
	var proxySawAuth string
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxySawRequest = true
		proxySawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"total_balance":50,"top_up_credits":40,"bonus_credits":10}`))
	}))
	defer proxyServer.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	// 目标地址不存在；只有真正走代理才能成功。
	row, err := service.Create(context.Background(), CreateRequest{
		Name:     "代理凭证",
		APIKey:   "sk-proxy-key-12345",
		Endpoint: "http://127.0.0.1:1/balance",
		ProxyURL: proxyServer.URL,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	verified, err := service.Verify(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !proxySawRequest {
		t.Fatal("expected request to go through explicit proxy")
	}
	if proxySawAuth != "Bearer sk-proxy-key-12345" {
		t.Fatalf("expected proxy to receive Bearer header, got %q", proxySawAuth)
	}
	if verified.CheckStatus != entities.ZenMuxCredentialCheckStatusSuccess || verified.TotalBalance == nil || *verified.TotalBalance != 50 {
		t.Fatalf("unexpected verified row: %+v", verified)
	}
}

func TestServiceCreateValidatesAndDefaults(t *testing.T) {
	db := openZenMuxTestDB(t)
	seedZenMuxIdentity(t, db, "auth-1", entities.UsageIdentityAuthTypeAuthFile)
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
	for _, proxyURL := range []string{"ftp://example.com", "not-a-url"} {
		if _, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", ProxyURL: proxyURL}); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected validation error for proxy_url %q, got %v", proxyURL, err)
		}
	}
	// auth_type 不能脱离 auth_index。
	authType := entities.UsageIdentityAuthTypeAuthFile
	if _, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", AuthType: &authType}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for orphan auth_type, got %v", err)
	}
	// 绑定的身份不存在。
	if _, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", AuthIndex: stringPtr("missing-auth")}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for missing identity, got %v", err)
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
	if row.ProxyURL != "" {
		t.Fatalf("expected empty proxy url, got %q", row.ProxyURL)
	}
	if row.AuthIndex == nil || *row.AuthIndex != "auth-1" || row.BoundAuthType == nil || *row.BoundAuthType != int(entities.UsageIdentityAuthTypeAuthFile) {
		t.Fatalf("expected auth file binding, got %+v / %+v", row.AuthIndex, row.BoundAuthType)
	}
	if row.CheckStatus != "" || row.CheckError != "" || row.CheckedAt != nil {
		t.Fatalf("expected fresh row to be never-checked: %+v", row)
	}
	if APIKeyPreview(row.APIKey) != "****" {
		t.Fatalf("unexpected preview %q", APIKeyPreview(row.APIKey))
	}
}

func TestServiceCreateBindsAIProviderType(t *testing.T) {
	db := openZenMuxTestDB(t)
	seedZenMuxIdentity(t, db, "provider-key-1", entities.UsageIdentityAuthTypeAIProvider)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	authType := entities.UsageIdentityAuthTypeAIProvider
	row, err := service.Create(context.Background(), CreateRequest{
		Name:      "AI 提供商",
		APIKey:    "sk-ai-key-123456",
		AuthIndex: stringPtr("provider-key-1"),
		AuthType:  &authType,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if row.AuthIndex == nil || *row.AuthIndex != "provider-key-1" || row.BoundAuthType == nil || *row.BoundAuthType != int(entities.UsageIdentityAuthTypeAIProvider) {
		t.Fatalf("expected ai provider binding, got %+v / %+v", row.AuthIndex, row.BoundAuthType)
	}
}

func TestServiceUpdateSemantics(t *testing.T) {
	db := openZenMuxTestDB(t)
	seedZenMuxIdentity(t, db, "auth-1", entities.UsageIdentityAuthTypeAuthFile)
	seedZenMuxIdentity(t, db, "auth-2", entities.UsageIdentityAuthTypeAuthFile)
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{Name: "主账号", APIKey: "sk-123456", Endpoint: "https://custom.example.com/balance", AuthIndex: stringPtr("auth-1")})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// 空 api_key 表示不修改；name 更新生效；auth_index 显式 null 解除绑定并清空类型。
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
	if updated.AuthIndex != nil || updated.BoundAuthType != nil {
		t.Fatalf("expected binding cleared, got %+v / %+v", updated.AuthIndex, updated.BoundAuthType)
	}

	// 重新绑定：auth_index 提供时设置，缺省 auth_type 按 Auth File。
	rebound, err := service.Update(context.Background(), row.ID, UpdateRequest{AuthIndex: stringPtr("auth-2"), AuthIndexSet: true})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if rebound.AuthIndex == nil || *rebound.AuthIndex != "auth-2" || rebound.BoundAuthType == nil || *rebound.BoundAuthType != int(entities.UsageIdentityAuthTypeAuthFile) {
		t.Fatalf("expected auth file binding to auth-2, got %+v / %+v", rebound.AuthIndex, rebound.BoundAuthType)
	}

	// 未提供 auth_index 时保持原值；endpoint 空串回退默认；proxy_url 可清空。
	kept, err := service.Update(context.Background(), row.ID, UpdateRequest{Endpoint: stringPtr(""), ProxyURL: stringPtr("")})
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
	// auth_type 不能脱离 auth_index。
	authType := entities.UsageIdentityAuthTypeAIProvider
	if _, err := service.Update(context.Background(), row.ID, UpdateRequest{AuthType: &authType, AuthTypeSet: true}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for orphan auth_type, got %v", err)
	}
	// 绑定到不存在的身份。
	if _, err := service.Update(context.Background(), row.ID, UpdateRequest{AuthIndex: stringPtr("missing"), AuthIndexSet: true}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for missing identity, got %v", err)
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

func TestServiceStatsByAuthIndexesMatchesTypePair(t *testing.T) {
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
	// 同一 identity 的 AI Provider 类型行必须按类型区分，不混入 Auth File 统计。
	if err := db.Create(&entities.UsageIdentity{
		Name:          "Provider Key One",
		AuthType:      entities.UsageIdentityAuthTypeAIProvider,
		AuthTypeName:  "apikey",
		Identity:      "auth-1",
		Type:          "openai",
		TotalRequests: 999,
		SuccessCount:  999,
	}).Error; err != nil {
		t.Fatalf("seed provider identity: %v", err)
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

	stats, err := service.StatsByAuthIndexes(context.Background(), []AuthBinding{
		{AuthIndex: "auth-1", AuthType: entities.UsageIdentityAuthTypeAuthFile},
		{AuthIndex: "auth-1", AuthType: entities.UsageIdentityAuthTypeAIProvider},
		{AuthIndex: "auth-missing", AuthType: entities.UsageIdentityAuthTypeAuthFile},
		{AuthIndex: "auth-deleted", AuthType: entities.UsageIdentityAuthTypeAuthFile},
	})
	if err != nil {
		t.Fatalf("StatsByAuthIndexes returned error: %v", err)
	}
	item, ok := stats[AuthBinding{AuthIndex: "auth-1", AuthType: entities.UsageIdentityAuthTypeAuthFile}]
	if !ok {
		t.Fatalf("expected stats for auth file binding, got %+v", stats)
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
	providerItem, ok := stats[AuthBinding{AuthIndex: "auth-1", AuthType: entities.UsageIdentityAuthTypeAIProvider}]
	if !ok || providerItem.TotalRequests != 999 {
		t.Fatalf("expected ai provider stats isolated, got %+v", stats)
	}
	if _, ok := stats[AuthBinding{AuthIndex: "auth-missing", AuthType: entities.UsageIdentityAuthTypeAuthFile}]; ok {
		t.Fatalf("expected missing auth index to be absent, got %+v", stats)
	}
	if _, ok := stats[AuthBinding{AuthIndex: "auth-deleted", AuthType: entities.UsageIdentityAuthTypeAuthFile}]; ok {
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
	stats, err := service.StatsByAuthIndexes(context.Background(), []AuthBinding{{AuthIndex: "auth-empty", AuthType: entities.UsageIdentityAuthTypeAuthFile}})
	if err != nil {
		t.Fatalf("StatsByAuthIndexes returned error: %v", err)
	}
	item := stats[AuthBinding{AuthIndex: "auth-empty", AuthType: entities.UsageIdentityAuthTypeAuthFile}]
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
	// Windows 上未关闭的 SQLite 句柄会锁住文件，所有测试库都必须在结束前关闭。
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&entities.ZenMuxCredential{}, &entities.UsageIdentity{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return db
}

func seedZenMuxIdentity(t *testing.T, db *gorm.DB, identity string, authType entities.UsageIdentityAuthType) {
	t.Helper()
	name := "Auth " + identity
	authTypeName := "oauth"
	identityType := "claude"
	if authType == entities.UsageIdentityAuthTypeAIProvider {
		name = "Provider " + identity
		authTypeName = "apikey"
		identityType = "openai"
	}
	if err := db.Create(&entities.UsageIdentity{
		Name:         name,
		AuthType:     authType,
		AuthTypeName: authTypeName,
		Identity:     identity,
		Type:         identityType,
	}).Error; err != nil {
		t.Fatalf("seed usage identity: %v", err)
	}
}

func stringPtr(value string) *string {
	return &value
}
func TestSubscriptionDetailURLReplacesPath(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		want     string
	}{
		{endpoint: "https://zenmux.ai/api/v1/management/payg/balance", want: "https://zenmux.ai/api/v1/management/subscription/detail"},
		{endpoint: "https://zenmux.ai/api/v1/management/payg/balance?x=1", want: "https://zenmux.ai/api/v1/management/subscription/detail"},
		{endpoint: "http://127.0.0.1:8080/custom/balance", want: "http://127.0.0.1:8080/api/v1/management/subscription/detail"},
	} {
		got, err := subscriptionDetailURL(test.endpoint)
		if err != nil {
			t.Fatalf("subscriptionDetailURL(%q) returned error: %v", test.endpoint, err)
		}
		if got != test.want {
			t.Fatalf("subscriptionDetailURL(%q) = %q, want %q", test.endpoint, got, test.want)
		}
	}
	if _, err := subscriptionDetailURL("://bad"); err == nil {
		t.Fatal("expected error for invalid endpoint")
	}
}

func TestParseSubscriptionResponseOfficialShape(t *testing.T) {
	body := `{"success":true,"data":{"plan":{"tier":"ultra","amount_usd":200,"interval":"month","expires_at":"2027-08-29T00:00:00Z"},"currency":"usd","base_usd_per_flow":0.03283,"effective_usd_per_flow":0.03283,"account_status":"healthy","quota_5_hour":{"usage_percentage":0.0715,"resets_at":"2026-08-29T15:00:00Z","max_flows":800,"used_flows":57.2,"remaining_flows":742.8,"used_value_usd":1.88,"max_value_usd":26.27},"quota_7_day":{"usage_percentage":0.5,"resets_at":"2026-09-01T00:00:00Z","max_flows":1600,"used_flows":800,"remaining_flows":800},"quota_monthly":{"max_flows":10000,"max_value_usd":328.3}}}`
	subscription, err := parseSubscriptionResponse([]byte(body))
	if err != nil {
		t.Fatalf("parseSubscriptionResponse returned error: %v", err)
	}
	if subscription.PlanTier != "ultra" || subscription.AccountStatus != "healthy" {
		t.Fatalf("unexpected plan/status: %+v", subscription)
	}
	if subscription.PlanExpiresAt == nil || *subscription.PlanExpiresAt != "2027-08-29T00:00:00Z" {
		t.Fatalf("unexpected plan expiry: %+v", subscription.PlanExpiresAt)
	}
	if subscription.Quota5Hour == nil || math.Abs(subscription.Quota5Hour.UsagePercentage-0.0715) > 1e-9 || subscription.Quota5Hour.MaxFlows != 800 || subscription.Quota5Hour.UsedFlows != 57.2 || subscription.Quota5Hour.RemainingFlows != 742.8 {
		t.Fatalf("unexpected 5h quota: %+v", subscription.Quota5Hour)
	}
	if subscription.Quota5Hour.ResetsAt == nil || *subscription.Quota5Hour.ResetsAt != "2026-08-29T15:00:00Z" {
		t.Fatalf("unexpected 5h reset: %+v", subscription.Quota5Hour.ResetsAt)
	}
	if subscription.Quota5Hour.UsedValueUSD == nil || math.Abs(*subscription.Quota5Hour.UsedValueUSD-1.88) > 1e-9 {
		t.Fatalf("unexpected 5h used value: %+v", subscription.Quota5Hour.UsedValueUSD)
	}
	if subscription.Quota5Hour.MaxValueUSD == nil || math.Abs(*subscription.Quota5Hour.MaxValueUSD-26.27) > 1e-9 {
		t.Fatalf("unexpected 5h max value: %+v", subscription.Quota5Hour.MaxValueUSD)
	}
	if subscription.Quota7Day == nil || math.Abs(subscription.Quota7Day.UsagePercentage-0.5) > 1e-9 || subscription.Quota7Day.MaxFlows != 1600 {
		t.Fatalf("unexpected 7d quota: %+v", subscription.Quota7Day)
	}
	if subscription.Quota7Day.UsedValueUSD != nil || subscription.Quota7Day.MaxValueUSD != nil {
		t.Fatalf("7d value fields should stay nil when absent: %+v", subscription.Quota7Day)
	}
	if subscription.QuotaMonthly == nil || subscription.QuotaMonthly.MaxFlows != 10000 || subscription.QuotaMonthly.MaxValueUSD != 328.3 {
		t.Fatalf("unexpected monthly quota: %+v", subscription.QuotaMonthly)
	}

	for _, bad := range []string{`{"success":true}`, `not-json`, `{"data":{"account_status":""}}`} {
		if _, err := parseSubscriptionResponse([]byte(bad)); err == nil {
			t.Fatalf("expected parse error for body %q", bad)
		}
	}
}

func TestServiceVerifyPersistsSubscription(t *testing.T) {
	db := openZenMuxTestDB(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/management/payg/balance", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"total_credits":482.74,"top_up_credits":35,"bonus_credits":447.74}}`))
	})
	mux.HandleFunc("/api/v1/management/subscription/detail", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-sub-key-123456" {
			t.Fatalf("expected Bearer header on subscription request, got %q", got)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"plan":{"tier":"ultra","expires_at":"2027-08-29T00:00:00Z"},"account_status":"healthy","quota_monthly":{"max_flows":10000,"max_value_usd":328.3}}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{
		Name:     "订阅凭证",
		APIKey:   "sk-sub-key-123456",
		Endpoint: server.URL + "/api/v1/management/payg/balance",
	})
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
	if verified.TotalBalance == nil || *verified.TotalBalance != 482.74 {
		t.Fatalf("expected total_credits mapped to total balance, got %+v", verified.TotalBalance)
	}
	if verified.SubscriptionJSON == nil || !strings.Contains(*verified.SubscriptionJSON, `"plan_tier":"ultra"`) || !strings.Contains(*verified.SubscriptionJSON, `"account_status":"healthy"`) {
		t.Fatalf("expected normalized subscription stored, got %+v", verified.SubscriptionJSON)
	}
}

func TestServiceVerifySubscriptionFailureKeepsSuccess(t *testing.T) {
	db := openZenMuxTestDB(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/management/payg/balance", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"total_credits":100}}`))
	})
	mux.HandleFunc("/api/v1/management/subscription/detail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	row, err := service.Create(context.Background(), CreateRequest{
		Name:     "无订阅凭证",
		APIKey:   "sk-sub-key-123456",
		Endpoint: server.URL + "/api/v1/management/payg/balance",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	verified, err := service.Verify(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified.CheckStatus != entities.ZenMuxCredentialCheckStatusSuccess {
		t.Fatalf("expected success status despite subscription 404, got %q error=%q", verified.CheckStatus, verified.CheckError)
	}
	if verified.SubscriptionJSON != nil {
		t.Fatalf("expected subscription NULL on 404, got %+v", verified.SubscriptionJSON)
	}

	// 订阅解析失败同样不影响 balance 成功。
	parseMux := http.NewServeMux()
	parseMux.HandleFunc("/api/v1/management/payg/balance", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"total_credits":100}}`))
	})
	parseMux.HandleFunc("/api/v1/management/subscription/detail", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"unexpected":"shape"}`))
	})
	parseServer := httptest.NewServer(parseMux)
	defer parseServer.Close()
	row2, err := service.Create(context.Background(), CreateRequest{
		Name:     "坏订阅响应凭证",
		APIKey:   "sk-sub-key-123456",
		Endpoint: parseServer.URL + "/api/v1/management/payg/balance",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	verified2, err := service.Verify(context.Background(), row2.ID)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if verified2.CheckStatus != entities.ZenMuxCredentialCheckStatusSuccess || verified2.SubscriptionJSON != nil {
		t.Fatalf("expected success with NULL subscription on parse failure, got %+v", verified2)
	}
}
