package quota

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"cpa-usage-keeper/internal/repository"

	"gorm.io/gorm"
)

type Service struct {
	db       *gorm.DB
	registry ProviderRegistry
}

type CheckRequest struct {
	AuthIndex string `json:"auth_index"`
}

type CheckResponse struct {
	ID    string     `json:"id"`
	Quota []QuotaRow `json:"quota"`
}

func NewService(db *gorm.DB, caller ManagementAPICaller) *Service {
	return NewServiceWithRegistry(db, NewDefaultProviderRegistry(caller, DefaultProviderConfigs()))
}

func NewServiceWithRegistry(db *gorm.DB, registry ProviderRegistry) *Service {
	return &Service{db: db, registry: registry}
}

func (s *Service) Check(ctx context.Context, request CheckRequest) (CheckResponse, error) {
	authIndex := strings.TrimSpace(request.AuthIndex)
	if authIndex == "" {
		return CheckResponse{}, fmt.Errorf("%w: auth_index is required", ErrValidation)
	}
	identity, err := repository.GetActiveAuthFileUsageIdentityByAuthIndex(ctx, s.db, authIndex)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CheckResponse{}, fmt.Errorf("%w: %s", ErrNotFound, authIndex)
		}
		return CheckResponse{}, err
	}
	_, handler, ok := s.resolveQuotaHandler(identity.Provider, identity.Type)
	if !ok {
		return CheckResponse{}, fmt.Errorf("%w: %s", ErrUnsupportedType, normalizeIdentityType(identity.Provider))
	}
	providerOutput, err := handler.Check(ctx, ProviderInput{Identity: identity})
	if err != nil {
		return CheckResponse{}, err
	}
	return CheckResponse{
		ID:    authIndex,
		Quota: NormalizeQuotaRows(providerOutput),
	}, nil
}

func (s *Service) resolveQuotaHandler(provider string, identityType string) (string, ProviderHandler, bool) {
	for _, candidate := range resolveQuotaIdentityTypes(provider, identityType) {
		if handler, ok := s.registry.Provider(candidate); ok {
			return candidate, handler, true
		}
	}
	return "", nil, false
}

func resolveQuotaIdentityTypes(provider string, identityType string) []string {
	candidates := make([]string, 0, 2)
	for _, value := range []string{provider, identityType} {
		normalized := normalizeIdentityType(value)
		if normalized == "" || slices.Contains(candidates, normalized) {
			continue
		}
		candidates = append(candidates, normalized)
	}
	return candidates
}
