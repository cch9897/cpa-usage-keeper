package test

import (
	"context"
	"sync"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	. "cpa-usage-keeper/internal/quota"

	"gorm.io/gorm"
)

// zenMuxVerifierStub 记录 VerifyAll 调用次数，可按需阻塞以观察巡检 running/completed 的门控时序。
type zenMuxVerifierStub struct {
	mu      sync.Mutex
	calls   int
	block   <-chan struct{}
	entered chan struct{}
}

func (s *zenMuxVerifierStub) VerifyAll(ctx context.Context) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.block:
		}
	}
	return nil
}

func (s *zenMuxVerifierStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newZenMuxVerifierStub(block <-chan struct{}) *zenMuxVerifierStub {
	return &zenMuxVerifierStub{block: block, entered: make(chan struct{}, 8)}
}

func waitForZenMuxVerifyStart(t *testing.T, verifier *zenMuxVerifierStub) {
	t.Helper()
	select {
	case <-verifier.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for zenmux VerifyAll to start")
	}
}

func waitForInspectionStatus(t *testing.T, service *Service, match func(InspectionStatus) bool) InspectionStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var status InspectionStatus
	var err error
	for time.Now().Before(deadline) {
		status, err = service.GetInspectionStatus(context.Background())
		if err == nil && match(status) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inspection status did not reach expected state, last=%+v err=%v", status, err)
	return InspectionStatus{}
}

func seedZenMuxCredential(t *testing.T, db *gorm.DB, credential entities.ZenMuxCredential) entities.ZenMuxCredential {
	t.Helper()
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	}
	credential.UpdatedAt = credential.CreatedAt
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("seed zenmux credential: %v", err)
	}
	return credential
}

func TestStartInspectionTriggersZenMuxVerifyAndDerivesStatus(t *testing.T) {
	db := openQuotaTestDatabase(t)
	checkedOld := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	checkedNew := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{
		Name: "旧成功", APIKey: "sk-old", Endpoint: "https://zenmux.example.com/balance",
		CheckStatus: entities.ZenMuxCredentialCheckStatusSuccess, CheckedAt: &checkedOld, TotalBalance: floatPtr(100),
	})
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{
		Name: "新失败", APIKey: "sk-failed", Endpoint: "https://zenmux.example.com/balance",
		CheckStatus: entities.ZenMuxCredentialCheckStatusFailed, CheckedAt: &checkedNew, CheckError: "HTTP 401: invalid api key",
	})
	// 与“新失败”同 checked_at，检验同时间按 id 升序的稳定排序。
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{
		Name: "同时成功", APIKey: "sk-tied", Endpoint: "https://zenmux.example.com/balance",
		CheckStatus: entities.ZenMuxCredentialCheckStatusSuccess, CheckedAt: &checkedNew, TotalBalance: floatPtr(50),
	})
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{
		Name: "未验证", APIKey: "sk-pending", Endpoint: "https://zenmux.example.com/balance",
	})
	verifier := newZenMuxVerifierStub(nil)
	service := newQuotaServiceWithRegistryAndOptions(t, db, NewProviderRegistry(nil), ServiceOptions{ZenMuxVerifier: verifier})

	if _, err := service.StartInspection(context.Background()); err != nil {
		t.Fatalf("StartInspection returned error: %v", err)
	}
	final := waitForInspectionStatus(t, service, func(status InspectionStatus) bool { return status.Completed })
	if verifier.callCount() != 1 {
		t.Fatalf("expected one VerifyAll call, got %d", verifier.callCount())
	}

	zenmux := final.Zenmux
	if zenmux == nil {
		t.Fatal("expected zenmux inspection block, got nil")
	}
	// stub 不持久化任何结果，统计必须完全由种子凭证行派生。
	if zenmux.Total != 4 || zenmux.Cached != 3 || zenmux.Success != 2 || zenmux.Failed != 1 || zenmux.Unknown != 1 || zenmux.Running {
		t.Fatalf("unexpected zenmux status counters: %+v", zenmux)
	}
	// Results 按 checked_at 倒序；“新失败”与“同时成功”同时间，按 id 升序排列。
	if len(zenmux.Results) != 3 {
		t.Fatalf("expected three zenmux results (unknown credential excluded), got %+v", zenmux.Results)
	}
	if zenmux.Results[0].Name != "新失败" || zenmux.Results[1].Name != "同时成功" || zenmux.Results[2].Name != "旧成功" {
		t.Fatalf("unexpected zenmux result order: %+v", zenmux.Results)
	}
	if zenmux.Results[0].Status != entities.ZenMuxCredentialCheckStatusFailed || zenmux.Results[0].Error != "HTTP 401: invalid api key" || zenmux.Results[0].CheckedAt == nil {
		t.Fatalf("unexpected failed zenmux result: %+v", zenmux.Results[0])
	}
	if zenmux.Results[2].TotalBalance == nil || *zenmux.Results[2].TotalBalance != 100 {
		t.Fatalf("expected total balance passthrough, got %+v", zenmux.Results[2])
	}
}

func TestZenMuxVerifyGatesInspectionCompletion(t *testing.T) {
	db := openQuotaTestDatabase(t)
	seedUsageIdentity(t, db, entities.UsageIdentity{Identity: "auth-1", Provider: "claude", Type: "claude", AuthType: entities.UsageIdentityAuthTypeAuthFile})
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{Name: "凭证", APIKey: "sk-gated", Endpoint: "https://zenmux.example.com/balance"})
	handler := &refreshHandlerStub{output: ProviderOutput{Result: ClaudeResult{Usage: &ClaudeUsagePayload{FiveHour: &ClaudeUsageWindow{Utilization: 25}}}}}
	block := make(chan struct{})
	verifier := newZenMuxVerifierStub(block)
	service := newQuotaServiceWithRegistryAndOptions(t, db, NewProviderRegistry(map[string]ProviderHandler{"claude": handler}), ServiceOptions{ZenMuxVerifier: verifier})
	setRefreshCooldown(service, func(time.Duration) {})

	if _, err := service.StartInspection(context.Background()); err != nil {
		t.Fatalf("StartInspection returned error: %v", err)
	}
	waitForRefreshTask(t, service, "auth-1", RefreshTaskStatusCompleted)

	// Auth Files 部分已 settle，但 ZenMux 验证仍阻塞：整轮必须保持 running，不能判定完成。
	gated, err := service.GetInspectionStatus(context.Background())
	if err != nil {
		t.Fatalf("GetInspectionStatus returned error: %v", err)
	}
	if !gated.Running || gated.Completed || gated.CompletedAt != nil {
		t.Fatalf("expected zenmux verify to gate inspection completion, got %+v", gated)
	}
	if gated.Zenmux == nil || !gated.Zenmux.Running {
		t.Fatalf("expected zenmux block to report running, got %+v", gated.Zenmux)
	}

	close(block)
	final := waitForInspectionStatus(t, service, func(status InspectionStatus) bool { return status.Completed })
	if final.Running || final.CompletedAt == nil || final.CompletedAt.IsZero() {
		t.Fatalf("expected completed inspection after zenmux verify settled, got %+v", final)
	}
	if verifier.callCount() != 1 {
		t.Fatalf("expected one VerifyAll call, got %d", verifier.callCount())
	}
}

func TestZenMuxOnlyInspectionRoundRunsAndCompletes(t *testing.T) {
	db := openQuotaTestDatabase(t)
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{Name: "唯一凭证", APIKey: "sk-only", Endpoint: "https://zenmux.example.com/balance"})
	block := make(chan struct{})
	verifier := newZenMuxVerifierStub(block)
	service := newQuotaServiceWithRegistryAndOptions(t, db, NewProviderRegistry(nil), ServiceOptions{ZenMuxVerifier: verifier})

	// 没有任何 Auth Files，仅有 ZenMux 凭证的巡检轮次同样要经历 Running→Completed。
	status, err := service.StartInspection(context.Background())
	if err != nil {
		t.Fatalf("StartInspection returned error: %v", err)
	}
	if status.Total != 0 || !status.Running || status.Completed {
		t.Fatalf("expected running zenmux-only inspection, got %+v", status)
	}
	if status.Zenmux == nil || !status.Zenmux.Running || status.Zenmux.Total != 1 || status.Zenmux.Unknown != 1 {
		t.Fatalf("unexpected zenmux block: %+v", status.Zenmux)
	}

	close(block)
	final := waitForInspectionStatus(t, service, func(status InspectionStatus) bool { return status.Completed })
	if final.Running || final.CompletedAt == nil {
		t.Fatalf("expected completed zenmux-only inspection, got %+v", final)
	}
	if verifier.callCount() != 1 {
		t.Fatalf("expected one VerifyAll call, got %d", verifier.callCount())
	}
}

func TestScheduledZenMuxVerifyDoesNotDriveInspectionState(t *testing.T) {
	db := openQuotaTestDatabase(t)
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{Name: "凭证", APIKey: "sk-scheduled", Endpoint: "https://zenmux.example.com/balance"})
	block := make(chan struct{})
	verifier := newZenMuxVerifierStub(block)
	service := newQuotaServiceWithRegistryAndOptions(t, db, NewProviderRegistry(nil), ServiceOptions{ZenMuxVerifier: verifier})

	if err := service.RunAutoRefresh(context.Background()); err != nil {
		t.Fatalf("RunAutoRefresh returned error: %v", err)
	}
	waitForZenMuxVerifyStart(t, verifier)

	// 定时刷新触发的 ZenMux 验证即使还在跑，也不能点亮巡检 running/completed。
	status, err := service.GetInspectionStatus(context.Background())
	if err != nil {
		t.Fatalf("GetInspectionStatus returned error: %v", err)
	}
	if status.Running || status.Completed || status.CompletedAt != nil {
		t.Fatalf("expected scheduled zenmux verify to leave inspection idle, got %+v", status)
	}
	if status.Zenmux == nil || status.Zenmux.Running {
		t.Fatalf("expected zenmux block without running flag, got %+v", status.Zenmux)
	}

	close(block)
	after, err := service.GetInspectionStatus(context.Background())
	if err != nil {
		t.Fatalf("GetInspectionStatus returned error: %v", err)
	}
	if after.Running || after.Completed || after.CompletedAt != nil {
		t.Fatalf("expected inspection to stay idle after scheduled verify, got %+v", after)
	}
	if verifier.callCount() != 1 {
		t.Fatalf("expected one VerifyAll call, got %d", verifier.callCount())
	}
}

func TestStartInspectionAdoptsRunningScheduledZenMuxVerify(t *testing.T) {
	db := openQuotaTestDatabase(t)
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{Name: "凭证", APIKey: "sk-adopted", Endpoint: "https://zenmux.example.com/balance"})
	block := make(chan struct{})
	verifier := newZenMuxVerifierStub(block)
	service := newQuotaServiceWithRegistryAndOptions(t, db, NewProviderRegistry(nil), ServiceOptions{ZenMuxVerifier: verifier})

	if err := service.RunAutoRefresh(context.Background()); err != nil {
		t.Fatalf("RunAutoRefresh returned error: %v", err)
	}
	waitForZenMuxVerifyStart(t, verifier)

	// scheduled 验证仍在跑：巡检收养这轮验证，不重复调用 VerifyAll，并把 running 保持到验证结束。
	status, err := service.StartInspection(context.Background())
	if err != nil {
		t.Fatalf("StartInspection returned error: %v", err)
	}
	if !status.Running || status.Completed {
		t.Fatalf("expected inspection to adopt running zenmux verify, got %+v", status)
	}
	if verifier.callCount() != 1 {
		t.Fatalf("expected inspection to adopt in-flight verify instead of a second call, got %d calls", verifier.callCount())
	}

	close(block)
	final := waitForInspectionStatus(t, service, func(status InspectionStatus) bool { return status.Completed })
	if final.CompletedAt == nil {
		t.Fatalf("expected adopted inspection round to complete, got %+v", final)
	}
	if verifier.callCount() != 1 {
		t.Fatalf("expected still one VerifyAll call after completion, got %d", verifier.callCount())
	}
}

func TestInspectionStatusOmitsZenMuxBlockWithoutVerifier(t *testing.T) {
	db := openQuotaTestDatabase(t)
	seedZenMuxCredential(t, db, entities.ZenMuxCredential{Name: "凭证", APIKey: "sk-no-verifier", Endpoint: "https://zenmux.example.com/balance"})
	service := newQuotaServiceWithRegistry(t, db, NewProviderRegistry(nil))

	// 即使存在 ZenMux 凭证，未配置 verifier 时巡检状态也不带 zenmux 块。
	status, err := service.GetInspectionStatus(context.Background())
	if err != nil {
		t.Fatalf("GetInspectionStatus returned error: %v", err)
	}
	if status.Zenmux != nil {
		t.Fatalf("expected nil zenmux block without verifier, got %+v", status.Zenmux)
	}
}
