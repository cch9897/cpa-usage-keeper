package zenmux

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	_ "cpa-usage-keeper/internal/timeutil"
)

// 余额处理器按 Authorization 头区分凭证；订阅详情请求直接 404，
// 订阅拉取是 best-effort，404 不影响余额验证结果。
func TestVerifyAllPersistsPerCredentialResults(t *testing.T) {
	db := openZenMuxTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "subscription") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer sk-good-key-123" {
			_, _ = w.Write([]byte(`{"total_balance":42,"top_up_credits":40,"bonus_credits":2}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid api key"))
	}))
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	good, err := service.Create(context.Background(), CreateRequest{Name: "好凭证", APIKey: "sk-good-key-123", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("Create good credential returned error: %v", err)
	}
	bad, err := service.Create(context.Background(), CreateRequest{Name: "坏凭证", APIKey: "sk-bad-key-123", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("Create bad credential returned error: %v", err)
	}

	if err := service.VerifyAll(context.Background()); err != nil {
		t.Fatalf("VerifyAll returned error: %v", err)
	}

	// 单个凭证 401 不中断整批：两行都必须各自持久化验证结果。
	var goodRow entities.ZenMuxCredential
	if err := db.First(&goodRow, good.ID).Error; err != nil {
		t.Fatalf("load good credential: %v", err)
	}
	if goodRow.CheckStatus != entities.ZenMuxCredentialCheckStatusSuccess || goodRow.CheckedAt == nil || goodRow.CheckError != "" {
		t.Fatalf("expected persisted success for good credential, got %+v", goodRow)
	}
	if goodRow.TotalBalance == nil || *goodRow.TotalBalance != 42 || goodRow.TopUpCredits == nil || *goodRow.TopUpCredits != 40 || goodRow.BonusCredits == nil || *goodRow.BonusCredits != 2 {
		t.Fatalf("expected persisted balances for good credential, got %+v", goodRow)
	}

	var badRow entities.ZenMuxCredential
	if err := db.First(&badRow, bad.ID).Error; err != nil {
		t.Fatalf("load bad credential: %v", err)
	}
	if badRow.CheckStatus != entities.ZenMuxCredentialCheckStatusFailed || badRow.CheckedAt == nil || badRow.TotalBalance != nil {
		t.Fatalf("expected persisted failure for bad credential, got %+v", badRow)
	}
	if badRow.CheckError != "HTTP 401: invalid api key" {
		t.Fatalf("unexpected persisted check error %q", badRow.CheckError)
	}
}

func TestVerifyAllEmptyTableMakesNoRequests(t *testing.T) {
	db := openZenMuxTestDB(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: time.Second})

	if err := service.VerifyAll(context.Background()); err != nil {
		t.Fatalf("VerifyAll returned error: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected no verification requests for empty table, got %d", got)
	}
}

func TestVerifyAllCapsConcurrency(t *testing.T) {
	db := openZenMuxTestDB(t)
	var inFlight atomic.Int32
	var peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "subscription") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		// 余额请求停留一段时间，让 7 个凭证的并发窗口充分重叠。
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"total_balance":1}`))
	}))
	defer server.Close()
	service := newServiceWithClient(db, &http.Client{Timeout: 5 * time.Second})

	for i := range 7 {
		if _, err := service.Create(context.Background(), CreateRequest{
			Name:     fmt.Sprintf("凭证-%d", i),
			APIKey:   fmt.Sprintf("sk-key-%08d", i),
			Endpoint: server.URL,
		}); err != nil {
			t.Fatalf("Create credential %d returned error: %v", i, err)
		}
	}

	if err := service.VerifyAll(context.Background()); err != nil {
		t.Fatalf("VerifyAll returned error: %v", err)
	}
	// 契约：批量验证并发上限是 3；下限 2 证明确实发生了并发，
	// 避免退化成顺序执行的实现也能通过本测试。上限用字面量而不是 verifyAllWorkerLimit，
	// 防止调大常量时断言边界跟着漂移。
	if observed := peak.Load(); observed > 3 || observed < 2 {
		t.Fatalf("expected peak concurrency in [2, 3], got %d", observed)
	}
	var succeeded int64
	if err := db.Model(&entities.ZenMuxCredential{}).Where("check_status = ?", entities.ZenMuxCredentialCheckStatusSuccess).Count(&succeeded).Error; err != nil {
		t.Fatalf("count succeeded credentials: %v", err)
	}
	if succeeded != 7 {
		t.Fatalf("expected all 7 credentials verified, got %d", succeeded)
	}
}
