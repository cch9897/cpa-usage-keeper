package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cpa-usage-keeper/internal/quota"
)

type quotaProviderStub struct {
	request  quota.CheckRequest
	response quota.CheckResponse
	err      error
}

func (s *quotaProviderStub) Check(ctx context.Context, request quota.CheckRequest) (quota.CheckResponse, error) {
	s.request = request
	if s.err != nil {
		return quota.CheckResponse{}, s.err
	}
	return s.response, nil
}

func floatPtr(value float64) *float64 {
	return &value
}

func TestQuotaCheckReturnsProviderResponse(t *testing.T) {
	provider := &quotaProviderStub{response: quota.CheckResponse{
		ID: "codex-auth",
		Quota: []quota.QuotaRow{{
			Key:       "rate_limit.primary_window",
			Label:     "5h",
			Scope:     "window",
			Remaining: floatPtr(10),
		}},
	}}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{"auth_index":"codex-auth"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if provider.request.AuthIndex != "codex-auth" {
		t.Fatalf("expected auth_index to be forwarded, got %+v", provider.request)
	}
	body := resp.Body.String()
	if !contains(body, `"id":"codex-auth"`) || !contains(body, `"quota":[`) || !contains(body, `"remaining":10`) || contains(body, `"auth_index"`) || contains(body, `"provider"`) || contains(body, `"type"`) || contains(body, `"result"`) || contains(body, `"planType"`) || contains(body, `"name"`) || contains(body, `"identity"`) || contains(body, `"account_id"`) || contains(body, `"project_id"`) {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func TestQuotaCheckReturnsProviderSpecificResultShape(t *testing.T) {
	provider := &quotaProviderStub{response: quota.CheckResponse{
		ID: "gemini-auth",
		Quota: []quota.QuotaRow{
			{Key: "bucket.gemini-2.5-pro_vertex.PROMPT", Label: "gemini-2.5-pro_vertex", Scope: "model", Metric: "PROMPT", RemainingFraction: floatPtr(0.7), Remaining: floatPtr(42), ResetAt: "2026-05-09T12:00:00Z"},
			{Key: "code_assist.current_tier.GOOGLE_ONE_AI", Label: "Code Assist Credit", Scope: "credits", Metric: "GOOGLE_ONE_AI", Remaining: floatPtr(10)},
		},
	}}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{"auth_index":"gemini-auth"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !contains(body, `"id":"gemini-auth"`) || !contains(body, `"bucket.gemini-2.5-pro_vertex.PROMPT"`) || !contains(body, `"code_assist.current_tier.GOOGLE_ONE_AI"`) || contains(body, `"quota_items"`) || contains(body, `"limits"`) || contains(body, `"auth_index"`) || contains(body, `"provider"`) || contains(body, `"type"`) {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func TestQuotaCheckRejectsMissingAuthIndex(t *testing.T) {
	provider := &quotaProviderStub{}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	if provider.request.AuthIndex != "" {
		t.Fatalf("provider should not be called for missing auth_index, got %+v", provider.request)
	}
}

func TestQuotaCheckMapsNotFoundTo404(t *testing.T) {
	provider := &quotaProviderStub{err: quota.ErrNotFound}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{"auth_index":"missing-auth"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestQuotaCheckMapsUnsupportedTypeTo422(t *testing.T) {
	provider := &quotaProviderStub{err: quota.ErrUnsupportedType}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{"auth_index":"unknown-auth"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestQuotaCheckMapsProviderInputTo422(t *testing.T) {
	provider := &quotaProviderStub{err: quota.ErrProviderInput}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{"auth_index":"codex-auth"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestQuotaDoesNotExposeProviderSpecificEndpoints(t *testing.T) {
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: &quotaProviderStub{}})
	paths := []string{
		"/api/v1/quota/antigravity",
		"/api/v1/quota/codex",
		"/api/v1/quota/gemini-cli",
		"/api/v1/quota/gemini-cli/code-assist",
		"/api/v1/quota/claude",
		"/api/v1/quota/kimi",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("expected %s to return 404, got %d", path, resp.Code)
		}
	}
}

func TestQuotaCheckMapsWrappedErrors(t *testing.T) {
	provider := &quotaProviderStub{err: errors.Join(quota.ErrNotFound, errors.New("missing"))}
	router := NewRouter(nil, nil, nil, nil, AuthConfig{}, nil, "", OptionalProviders{Quota: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quota/check", strings.NewReader(`{"auth_index":"missing-auth"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", resp.Code, resp.Body.String())
	}
}
