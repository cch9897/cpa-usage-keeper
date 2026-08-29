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
	statsBindings []zenmux.AuthBinding
	stats         map[zenmux.AuthBinding]zenmux.CredentialStats
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

func (s *zenMuxCredentialProviderStub) StatsByAuthIndexes(_ context.Context, bindings []zenmux.AuthBinding) (map[zenmux.AuthBinding]zenmux.CredentialStats, error) {
	s.statsBindings = bindings
	if s.statsErr != nil {
		return nil, s.statsErr
	}
	return s.stats, nil
}

func zenMuxTestCredential(id int64, authIndex *string, boundAuthType *int) entities.ZenMuxCredential {
	createdAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return entities.ZenMuxCredential{
		ID:            id,
		Name:          "主账号",
		APIKey:        "sk-m-1234567890abcd",
		Endpoint:      "https://zenmux.ai/api/v1/management/payg/balance",
		AuthIndex:     authIndex,
		BoundAuthType: boundAuthType,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}
}

func TestZenMuxCredentialsListReturnsContractShape(t *testing.T) {
	checkedAt := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	totalBalance := 12.34
	topUp := 10.0
	bonus := 2.34
	subscriptionJSON := `{"plan_tier":"ultra","plan_expires_at":"2027-08-29T00:00:00Z","account_status":"healthy","quota_5_hour":{"usage_percentage":0.0715,"used_flows":57.2,"remaining_flows":742.8,"max_flows":800,"resets_at":"2026-08-29T15:00:00Z"},"quota_7_day":null,"quota_monthly":{"max_flows":10000,"max_value_usd":328.3}}`
	authFileType := int(entities.UsageIdentityAuthTypeAuthFile)
	aiProviderType := int(entities.UsageIdentityAuthTypeAIProvider)
	provider := &zenMuxCredentialProviderStub{
		rows: []entities.ZenMuxCredential{
			{
				ID:               1,
				Name:             "主账号",
				APIKey:           "sk-m-1234567890abcd",
				Endpoint:         "https://zenmux.ai/api/v1/management/payg/balance",
				ProxyURL:         "http://127.0.0.1:7890",
				AuthIndex:        stringPointer("auth-xyz"),
				BoundAuthType:    &authFileType,
				CheckStatus:      entities.ZenMuxCredentialCheckStatusSuccess,
				CheckedAt:        &checkedAt,
				TotalBalance:     &totalBalance,
				TopUpCredits:     &topUp,
				BonusCredits:     &bonus,
				SubscriptionJSON: &subscriptionJSON,
				CreatedAt:        checkedAt,
				UpdatedAt:        checkedAt,
			},
			{
				ID:            2,
				Name:          "AI 提供商凭证",
				APIKey:        "sk-m-aaaabbbbccccdd",
				Endpoint:      "https://zenmux.ai/api/v1/management/payg/balance",
				AuthIndex:     stringPointer("provider-key-9"),
				BoundAuthType: &aiProviderType,
				CreatedAt:     checkedAt,
				UpdatedAt:     checkedAt,
			},
			{
				ID:        3,
				Name:      "未绑定凭证",
				APIKey:    "sk-m-eeeeffffgggghhhh",
				Endpoint:  "https://zenmux.ai/api/v1/management/payg/balance",
				CreatedAt: checkedAt,
				UpdatedAt: checkedAt,
			},
		},
		stats: map[zenmux.AuthBinding]zenmux.CredentialStats{
			{AuthIndex: "auth-xyz", AuthType: entities.UsageIdentityAuthTypeAuthFile}:         {TotalRequests: 100, SuccessCount: 98, FailureCount: 2, SuccessRate: 0.98, TotalTokens: 123456, CacheReadTokens: 30000, CacheReadRate: 0.24},
			{AuthIndex: "provider-key-9", AuthType: entities.UsageIdentityAuthTypeAIProvider}: {TotalRequests: 50, SuccessCount: 45, FailureCount: 5, SuccessRate: 0.9, TotalTokens: 5000, CacheReadTokens: 500, CacheReadRate: 0.1},
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
	if !contains(body, `"proxy_url":"http://127.0.0.1:7890"`) || !contains(body, `"proxy_url":""`) {
		t.Fatalf("expected proxy_url variants: %s", body)
	}
	if !contains(body, `"auth_index":"auth-xyz"`) || !contains(body, `"auth_index":"provider-key-9"`) || !contains(body, `"auth_index":null`) {
		t.Fatalf("expected auth_index bound and null variants: %s", body)
	}
	if !contains(body, `"auth_type":"auth-file"`) || !contains(body, `"auth_type":"ai-provider"`) || !contains(body, `"auth_type":null`) {
		t.Fatalf("expected auth_type variants: %s", body)
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
	if !contains(body, `"subscription":{"plan_tier":"ultra","plan_expires_at":"2027-08-29T00:00:00Z","account_status":"healthy","quota_5_hour":{"usage_percentage":0.0715,"used_flows":57.2,"remaining_flows":742.8,"max_flows":800,"resets_at":"2026-08-29T15:00:00Z"},"quota_7_day":null,"quota_monthly":{"max_flows":10000,"max_value_usd":328.3}}`) {
		t.Fatalf("expected subscription payload: %s", body)
	}
	if !contains(body, `"subscription":null`) {
		t.Fatalf("expected null subscription for rows without data: %s", body)
	}
	if !contains(body, `"created_at":"`+timeutil.FormatStorageTime(checkedAt)+`"`) || !contains(body, `"updated_at":"`+timeutil.FormatStorageTime(checkedAt)+`"`) {
		t.Fatalf("expected timestamps in response: %s", body)
	}
	wantBindings := "auth-xyz:1,provider-key-9:2"
	gotBindings := make([]string, 0, len(provider.statsBindings))
	for _, binding := range provider.statsBindings {
		gotBindings = append(gotBindings, binding.AuthIndex+":"+string(rune('0'+binding.AuthType)))
	}
	if strings.Join(gotBindings, ",") != wantBindings {
		t.Fatalf("expected stats lookup for bound auth indexes, got %q", strings.Join(gotBindings, ","))
	}
}

func TestZenMuxCredentialsCreateForwardsRequest(t *testing.T) {
	authFileType := int(entities.UsageIdentityAuthTypeAuthFile)
	created := zenMuxTestCredential(7, stringPointer("auth-1"), &authFileType)
	provider := &zenMuxCredentialProviderStub{
		created: created,
		stats: map[zenmux.AuthBinding]zenmux.CredentialStats{
			{AuthIndex: "auth-1", AuthType: entities.UsageIdentityAuthTypeAuthFile}: {TotalRequests: 10, SuccessCount: 9, FailureCount: 1, SuccessRate: 0.9, TotalTokens: 1000, CacheReadTokens: 200, CacheReadRate: 0.25},
		},
	}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials", strings.NewReader(`{"name":"主账号","api_key":"sk-m-1234567890abcd","endpoint":"https://custom.example.com/balance","proxy_url":"http://127.0.0.1:7890","auth_index":"auth-1","auth_type":"auth-file"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if provider.createRequest.Name != "主账号" || provider.createRequest.APIKey != "sk-m-1234567890abcd" || provider.createRequest.Endpoint != "https://custom.example.com/balance" || provider.createRequest.ProxyURL != "http://127.0.0.1:7890" || provider.createRequest.AuthIndex == nil || *provider.createRequest.AuthIndex != "auth-1" || provider.createRequest.AuthType == nil || *provider.createRequest.AuthType != entities.UsageIdentityAuthTypeAuthFile {
		t.Fatalf("unexpected create request: %+v", provider.createRequest)
	}
	body := resp.Body.String()
	if !contains(body, `"id":"7"`) || !contains(body, `"api_key_preview":"sk-m-****abcd"`) || contains(body, `"api_key":"sk-m-`) {
		t.Fatalf("unexpected create response: %s", body)
	}
	if !contains(body, `"proxy_url":""`) || !contains(body, `"auth_type":"auth-file"`) {
		t.Fatalf("expected v2 fields in create response: %s", body)
	}
	if !contains(body, `"stats":{"total_requests":10,"success_count":9,"failure_count":1,"success_rate":0.9,"total_tokens":1000,"cache_read_tokens":200,"cache_read_rate":0.25}`) {
		t.Fatalf("expected stats in create response: %s", body)
	}
}

func TestZenMuxCredentialsCreateDefaultsAuthFileType(t *testing.T) {
	provider := &zenMuxCredentialProviderStub{created: zenMuxTestCredential(1, stringPointer("auth-1"), intPointer(int(entities.UsageIdentityAuthTypeAuthFile)))}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	// v1 兼容：提供 auth_index 但不提供 auth_type，服务端按 auth-file 处理（由服务层决定，这里验证透传 nil）。
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials", strings.NewReader(`{"name":"主账号","api_key":"sk-m-1234567890abcd","auth_index":"auth-1"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if provider.createRequest.AuthType != nil {
		t.Fatalf("expected nil auth_type passthrough, got %+v", provider.createRequest.AuthType)
	}
	if provider.createRequest.AuthIndex == nil || *provider.createRequest.AuthIndex != "auth-1" {
		t.Fatalf("expected auth_index passthrough, got %+v", provider.createRequest.AuthIndex)
	}
}

func TestZenMuxCredentialsCreateRejectsInvalidAuthType(t *testing.T) {
	provider := &zenMuxCredentialProviderStub{created: zenMuxTestCredential(1, nil, nil)}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zenmux/credentials", strings.NewReader(`{"name":"主账号","api_key":"sk-m-1234567890abcd","auth_type":"nonsense"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid auth_type, got %d body=%s", resp.Code, resp.Body.String())
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
	updated := zenMuxTestCredential(1, nil, nil)
	provider := &zenMuxCredentialProviderStub{updated: updated}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{ZenMux: provider})

	// auth_index 显式 null：解除绑定（AuthIndexSet=true, AuthIndex=nil）。
	req := httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"name":"新名字","api_key":"","auth_index":null,"proxy_url":""}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !provider.updateRequest.AuthIndexSet || provider.updateRequest.AuthIndex != nil || provider.updateRequest.Name == nil || *provider.updateRequest.Name != "新名字" || provider.updateRequest.APIKey == nil || *provider.updateRequest.APIKey != "" || provider.updateRequest.ProxyURL == nil || *provider.updateRequest.ProxyURL != "" {
		t.Fatalf("expected explicit null auth_index with field passthrough, got %+v", provider.updateRequest)
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

	// auth_index 提供字符串 + auth_type：绑定为 ai-provider。
	req = httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"auth_index":"provider-key-9","auth_type":"ai-provider"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !provider.updateRequest.AuthIndexSet || provider.updateRequest.AuthIndex == nil || *provider.updateRequest.AuthIndex != "provider-key-9" || !provider.updateRequest.AuthTypeSet || provider.updateRequest.AuthType == nil || *provider.updateRequest.AuthType != entities.UsageIdentityAuthTypeAIProvider {
		t.Fatalf("expected ai-provider binding, got %+v", provider.updateRequest)
	}

	// auth_index 提供但 auth_type 缺省：AuthTypeSet=false（服务端按 auth-file 兜底）。
	req = httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"auth_index":"auth-1"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if !provider.updateRequest.AuthIndexSet || provider.updateRequest.AuthTypeSet {
		t.Fatalf("expected auth_type to stay unset, got %+v", provider.updateRequest)
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

	// auth_type 非法值：400。
	req = httptest.NewRequest(http.MethodPut, "/api/v1/zenmux/credentials/1", strings.NewReader(`{"auth_type":"nonsense"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid auth_type, got %d body=%s", resp.Code, resp.Body.String())
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
	subscriptionJSON := `{"plan_tier":"pro","plan_expires_at":null,"account_status":"healthy","quota_5_hour":null,"quota_7_day":null,"quota_monthly":{"max_flows":5000,"max_value_usd":150}}`
	verified := entities.ZenMuxCredential{
		ID:               5,
		Name:             "主账号",
		APIKey:           "sk-m-1234567890abcd",
		Endpoint:         "https://zenmux.ai/api/v1/management/payg/balance",
		CheckStatus:      entities.ZenMuxCredentialCheckStatusSuccess,
		CheckedAt:        &checkedAt,
		TotalBalance:     &totalBalance,
		TopUpCredits:     &topUp,
		BonusCredits:     &bonus,
		SubscriptionJSON: &subscriptionJSON,
		CreatedAt:        checkedAt,
		UpdatedAt:        checkedAt,
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
	if !contains(body, `"proxy_url":""`) || !contains(body, `"auth_type":null`) {
		t.Fatalf("expected v2 fields in verify response: %s", body)
	}
	if !contains(body, `"subscription":{"plan_tier":"pro","plan_expires_at":null,"account_status":"healthy","quota_5_hour":null,"quota_7_day":null,"quota_monthly":{"max_flows":5000,"max_value_usd":150}}`) {
		t.Fatalf("expected subscription in verify response: %s", body)
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

func intPointer(value int) *int {
	return &value
}
