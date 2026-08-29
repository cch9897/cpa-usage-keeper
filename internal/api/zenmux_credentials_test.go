package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"
	"cpa-usage-keeper/internal/zenmux"

	"gorm.io/gorm"
)

type zenMuxCredentialProviderStub struct {
	rows          []entities.ZenMuxCredential
	listErr       error
	createRequest zenmux.CreateRequest
	created       entities.ZenMuxCredential
	createErr     error
	updateRequest zenmux.UpdateRequest
	updated       entities.ZenMuxCredential
	updateErr     error
	deletedID     int64
	deleteErr     error
	verifyID      int64
	verified      entities.ZenMuxCredential
	verifyErr     error
	statsIndexes  []string
	stats         map[string]zenmux.CredentialStats
	statsErr      error
}

func (s *zenMuxCredentialProviderStub) List(context.Context) ([]entities.ZenMuxCredential, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.rows, nil
}

func (s *zenMuxCredentialProviderStub) Create(_ context.Context, request zenmux.CreateRequest) (entities.ZenMuxCredential, error) {
	s.createRequest = request
	if s.createErr != nil {
		return entities.ZenMuxCredential{}, s.createErr
	}
	return s.created, nil
}

func (s *zenMuxCredentialProviderStub) Update(_ context.Context, id int64, request zenmux.UpdateRequest) (entities.ZenMuxCredential, error) {
	s.updateRequest = request
	if s.updateErr != nil {
		return entities.ZenMuxCredential{}, s.updateErr
	}
	return s.updated, nil
}

func (s *zenMuxCredentialProviderStub) Delete(_ context.Context, id int64) error {
	s.deletedID = id
	return s.deleteErr
}

func (s *zenMuxCredentialProviderStub) Verify(_ context.Context, id int64) (entities.ZenMuxCredential, error) {
	s.verifyID = id
	if s.verifyErr != nil {
		return entities.ZenMuxCredential{}, s.verifyErr
	}
	return s.verified, nil
}

func (s *zenMuxCredentialProviderStub) StatsByAuthIndexes(_ context.Context, authIndexes []string) (map[string]zenmux.CredentialStats, error) {
	s.statsIndexes = authIndexes
	if s.statsErr != nil {
		return nil, s.statsErr
	}
	return s.stats, nil
}

func zenMuxTestCredential(id int64, authIndex *string) entities.ZenMuxCredential {
	createdAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return entities.ZenMuxCredential{
		ID:        id,
		Name:      "主账号",
		APIKey:    "sk-m-1234567890abcd",
		Endpoint:  "https://zenmux.ai/api/v1/management/payg/balance",
		AuthIndex: authIndex,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func TestZenMuxCredentialsListReturnsContractShape(t *testing.T) {
	checkedAt := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	totalBalance := 12.34
	topUp := 10.0
	bonus := 2.34
	provider := &zenMuxCredentialProviderStub{
		rows: []entities.ZenMuxCredential{
			{
				ID:           1,
				Name:         "主账号",
				APIKey:       "sk-m-1234567890abcd",
				Endpoint:     "https://zenmux.ai/api/v1/management/payg/balance",
				AuthIndex:    stringPointer("auth-xyz"),
				CheckStatus:  entities.ZenMuxCredentialCheckStatusSuccess,
				CheckedAt:    &checkedAt,
				TotalBalance: &totalBalance,
				TopUpCredits: &topUp,
				BonusCredits: &bonus,
				CreatedAt:    checkedAt,
				UpdatedAt:    checkedAt,
			},
			{
				ID:        2,
				Name:      "未绑定凭证",
				APIKey:    "sk-m-aaaabbbbccccdd",
				Endpoint:  "https://zenmux.ai/api/v1/management/payg/balance",
				CreatedAt: checkedAt,
				UpdatedAt: checkedAt,
			},
		},
		stats: map[string]zenmux.CredentialStats{
			"auth-xyz": {TotalRequests: 100, SuccessCount: 98, FailureCount: 2, SuccessRate: 0.98, TotalTokens: 123456, CacheReadTokens: 30000, CacheReadRate: 0.24},
		},
	}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/zenmux/credentials", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !contains(body, `"items":[`) {
		t.Fatalf("expected items array in response: %s", body)
	}
	if !contains(body, `"id":"1"`) || !contains(body, `"name":"主账号"`) {
		t.Fatalf("expected credential fields in response: %s", body)
	}
	if !contains(body, `"api_key_preview":"sk-m-****abcd"`) {
		t.Fatalf("expected masked api key preview in response: %s", body)
	}
	if contains(body, `"api_key":"sk-m-`) || contains(body, `"APIKey"`) {
		t.Fatalf("api_key must never be returned: %s", body)
	}
	if !contains(body, `"auth_index":"auth-xyz"`) || !contains(body, `"auth_index":null`) {
		t.Fatalf("expected auth_index bound and null variants: %s", body)
	}
	if !contains(body, `"check":{"status":"success","checked_at":"`+timeutil.FormatStorageTime(checkedAt)+`","total_balance":12.34,"top_up_credits":10,"bonus_credits":2.34,"error":null}`) {
		t.Fatalf("expected success check payload: %s", body)
	}
	if !contains(body, `"check":{"status":"never","checked_at":null,"total_balance":null,"top_up_credits":null,"bonus_credits":null,"error":null}`) {
		t.Fatalf("expected never check payload: %s", body)
	}
	if !contains(body, `"stats":{"total_requests":100,"success_count":98,"failure_count":2,"success_rate":0.98,"total_tokens":123456,"cache_read_tokens":30000,"cache_read_rate":0.24}`) {
		t.Fatalf("expected bound stats payload: %s", body)
	}
	if !contains(body, `"stats":null`) {
		t.Fatalf("expected null stats for unbound credential: %s", body)
	}
	if !contains(body, `"created_at":"`+timeutil.FormatStorageTime(checkedAt)+`"`) || !contains(body, `"updated_at":"`+timeutil.FormatStorageTime(checkedAt)+`"`) {
		t.Fatalf("expected timestamps in response: %s", body)
	}
	if got := strings.Join(provider.statsIndexes, ","); got != "auth-xyz" {
		t.Fatalf("expected stats lookup for bound auth index only, got %q", got)
	}
}

func TestZenMuxCredentialsCreateForwardsRequest(t *testing.T) {
	created := zenMuxTestCredential(7, stringPointer("auth-1"))
	provider := &zenMuxCredentialProviderStub{
		created: created,
		stats: map[string]zenmux.CredentialStats{
			"auth-1": {TotalRequests: 10, SuccessCount: 9, FailureCount: 1, SuccessRate: 0.9, TotalTokens: 1000, CacheReadTokens: 200, CacheReadRate: 0.25},
		},
	}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials", strings.NewReader(`{"name":"主账号","api_key":"sk-m-1234567890abcd","endpoint":"https://custom.example.com/balance","auth_index":"auth-1"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if provider.createRequest.Name != "主账号" || provider.createRequest.APIKey != "sk-m-1234567890abcd" || provider.createRequest.Endpoint != "https://custom.example.com/balance" || provider.createRequest.AuthIndex == nil || *provider.createRequest.AuthIndex != "auth-1" {
		t.Fatalf("unexpected create request: %+v", provider.createRequest)
	}
	body := resp.Body.String()
	if !contains(body, `"id":"7"`) || !contains(body, `"api_key_preview":"sk-m-****abcd"`) || contains(body, `"api_key":"sk-m-`) {
		t.Fatalf("unexpected create response: %s", body)
	}
	if !contains(body, `"stats":{"total_requests":10,"success_count":9,"failure_count":1,"success_rate":0.9,"total_tokens":1000,"cache_read_tokens":200,"cache_read_rate":0.25}`) {
		t.Fatalf("expected stats in create response: %s", body)
	}
}
func TestZenMuxCredentialsCreateMapsValidationError(t *testing.T) {
	provider := &zenMuxCredentialProviderStub{createErr: zenmux.ErrValidation}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials", strings.NewReader(`{"name":"","api_key":""}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !contains(resp.Body.String(), `"error":"zenmux credential validation failed"`) {
		t.Fatalf("expected validation error message: %s", resp.Body.String())
	}
}

func TestZenMuxCredentialsUpdateDistinguishesAbsentAndNullAuthIndex(t *testing.T) {
	updated := zenMuxTestCredential(1, nil)
	provider := &zenMuxCredentialProviderStub{updated: updated}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	// auth_index 显式 null：解除绑定（AuthIndexSet=true, AuthIndex=nil）。
	req := httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"name":"新名字","api_key":"","auth_index":null}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !provider.updateRequest.AuthIndexSet || provider.updateRequest.AuthIndex != nil || provider.updateRequest.Name == nil || *provider.updateRequest.Name != "新名字" || provider.updateRequest.APIKey == nil || *provider.updateRequest.APIKey != "" {
		t.Fatalf("expected explicit null auth_index with empty api_key passthrough, got %+v", provider.updateRequest)
	}

	// auth_index 未提供：保持原值（AuthIndexSet=false）。
	req = httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"name":"另一个名字"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if provider.updateRequest.AuthIndexSet {
		t.Fatalf("expected absent auth_index to keep original binding, got %+v", provider.updateRequest)
	}

	// auth_index 提供字符串：绑定。
	req = httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"auth_index":"auth-9"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !provider.updateRequest.AuthIndexSet || provider.updateRequest.AuthIndex == nil || *provider.updateRequest.AuthIndex != "auth-9" {
		t.Fatalf("expected auth_index auth-9, got %+v", provider.updateRequest)
	}

	// auth_index 非法类型：400。
	req = httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"auth_index":42}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid auth_index, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestZenMuxCredentialsDeleteReturnsOK(t *testing.T) {
	provider := &zenMuxCredentialProviderStub{}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/zenmux/credentials/3", nil)
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK || provider.deletedID != 3 {
		t.Fatalf("expected status 200 with deleted id 3, got %d id=%d body=%s", resp.Code, provider.deletedID, resp.Body.String())
	}
	if !contains(resp.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok response, got %s", resp.Body.String())
	}
}

func TestZenMuxCredentialsVerifyReturnsFreshCheck(t *testing.T) {
	checkedAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	totalBalance := 66.6
	topUp := 60.0
	bonus := 6.6
	verified := entities.ZenMuxCredential{
		ID:           5,
		Name:         "主账号",
		APIKey:       "sk-m-1234567890abcd",
		Endpoint:     "https://zenmux.ai/api/v1/management/payg/balance",
		CheckStatus:  entities.ZenMuxCredentialCheckStatusSuccess,
		CheckedAt:    &checkedAt,
		TotalBalance: &totalBalance,
		TopUpCredits: &topUp,
		BonusCredits: &bonus,
		CreatedAt:    checkedAt,
		UpdatedAt:    checkedAt,
	}
	provider := &zenMuxCredentialProviderStub{verified: verified}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials/5/verify", nil)
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK || provider.verifyID != 5 {
		t.Fatalf("expected status 200 with verify id 5, got %d id=%d body=%s", resp.Code, provider.verifyID, resp.Body.String())
	}
	body := resp.Body.String()
	if !contains(body, `"check":{"status":"success"`) || !contains(body, `"total_balance":66.6`) || !contains(body, `"top_up_credits":60`) || !contains(body, `"bonus_credits":6.6`) {
		t.Fatalf("expected fresh check payload: %s", body)
	}
}

func TestZenMuxCredentialsErrorMapping(t *testing.T) {
	notFound := &zenMuxCredentialProviderStub{verifyErr: gorm.ErrRecordNotFound}
	internal := &zenMuxCredentialProviderStub{listErr: errors.New("boom")}

	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: notFound})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials/9/verify", nil)
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for missing credential, got %d body=%s", resp.Code, resp.Body.String())
	}

	router = NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: internal})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/zenmux/credentials", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for internal error, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestZenMuxCredentialsRoutesWithNilProvider(t *testing.T) {
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/zenmux/credentials", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || !contains(resp.Body.String(), `"items":[]`) {
		t.Fatalf("expected empty items for nil provider, got %d body=%s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials", strings.NewReader(`{"name":"x","api_key":"y"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil provider create, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestZenMuxCredentialsRoutesRejectInvalidID(t *testing.T) {
	provider := &zenMuxCredentialProviderStub{}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPut, path: "/api/v1/zenmux/credentials/abc", body: `{}`},
		{method: http.MethodDelete, path: "/api/v1/zenmux/credentials/0"},
		{method: http.MethodPost, path: "/api/v1/zenmux/credentials/-1/verify"},
	} {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s %s, got %d body=%s", test.method, test.path, resp.Code, resp.Body.String())
		}
	}
}

func stringPointer(value string) *string {
	return &value
}
