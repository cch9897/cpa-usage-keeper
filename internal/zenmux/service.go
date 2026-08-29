package zenmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// ErrValidation 表示请求字段校验失败，API 层映射为 400。
var ErrValidation = errors.New("zenmux credential validation failed")

// CreateRequest 是创建 ZenMux 管理凭证的输入。
type CreateRequest struct {
	Name      string
	APIKey    string
	Endpoint  string
	ProxyURL  string
	AuthIndex *string
	// AuthType 仅在 AuthIndex 非 nil 时有意义；nil 时按 Auth File（1）处理。
	AuthType *entities.UsageIdentityAuthType
}

// UpdateRequest 是更新 ZenMux 管理凭证的输入；指针字段为 nil 表示不修改。
// AuthIndexSet 区分 auth_index 未提供（保持原值）与显式 null（解除绑定）。
type UpdateRequest struct {
	Name         *string
	APIKey       *string
	Endpoint     *string
	ProxyURL     *string
	AuthIndex    *string
	AuthIndexSet bool
	AuthType     *entities.UsageIdentityAuthType
	AuthTypeSet  bool
}

// CredentialStats 是绑定 Keeper usage identity 的本地统计快照。
type CredentialStats struct {
	TotalRequests   int64
	SuccessCount    int64
	FailureCount    int64
	SuccessRate     float64
	TotalTokens     int64
	CacheReadTokens int64
	CacheReadRate   float64
}

// AuthBinding 标识一条绑定的 usage identity（identity + auth_type 成对）。
type AuthBinding struct {
	AuthIndex string
	AuthType  entities.UsageIdentityAuthType
}

// Provider 是 ZenMux 管理凭证业务能力接口，供 API 层与测试 stub 使用。
type Provider interface {
	List(ctx context.Context) ([]entities.ZenMuxCredential, error)
	Create(ctx context.Context, request CreateRequest) (entities.ZenMuxCredential, error)
	Update(ctx context.Context, id int64, request UpdateRequest) (entities.ZenMuxCredential, error)
	Delete(ctx context.Context, id int64) error
	Verify(ctx context.Context, id int64) (entities.ZenMuxCredential, error)
	StatsByAuthIndexes(ctx context.Context, bindings []AuthBinding) (map[AuthBinding]CredentialStats, error)
}

type service struct {
	db         *gorm.DB
	httpClient *http.Client
}

// NewService 创建 ZenMux 凭证服务；验证请求使用 15s 超时的默认客户端。
func NewService(db *gorm.DB) Provider {
	return newServiceWithClient(db, &http.Client{Timeout: defaultVerifyTimeout})
}

func newServiceWithClient(db *gorm.DB, client *http.Client) Provider {
	return &service{db: db, httpClient: client}
}

func (s *service) List(ctx context.Context) ([]entities.ZenMuxCredential, error) {
	var rows []entities.ZenMuxCredential
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *service) Create(ctx context.Context, request CreateRequest) (entities.ZenMuxCredential, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return entities.ZenMuxCredential{}, fmt.Errorf("%w: name is required", ErrValidation)
	}
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		return entities.ZenMuxCredential{}, fmt.Errorf("%w: api_key is required", ErrValidation)
	}
	endpoint, err := normalizeEndpoint(request.Endpoint)
	if err != nil {
		return entities.ZenMuxCredential{}, err
	}
	proxyURL, err := normalizeProxyURL(request.ProxyURL)
	if err != nil {
		return entities.ZenMuxCredential{}, err
	}
	authIndex, authType, err := s.resolveBinding(ctx, request.AuthIndex, request.AuthType)
	if err != nil {
		return entities.ZenMuxCredential{}, err
	}

	row := entities.ZenMuxCredential{
		Name:          name,
		APIKey:        apiKey,
		Endpoint:      endpoint,
		ProxyURL:      proxyURL,
		AuthIndex:     authIndex,
		BoundAuthType: authType,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return entities.ZenMuxCredential{}, err
	}
	return row, nil
}

func (s *service) Update(ctx context.Context, id int64, request UpdateRequest) (entities.ZenMuxCredential, error) {
	if id <= 0 {
		return entities.ZenMuxCredential{}, fmt.Errorf("%w: invalid id", ErrValidation)
	}
	var row entities.ZenMuxCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return entities.ZenMuxCredential{}, err
	}

	updates := map[string]any{}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" {
			return entities.ZenMuxCredential{}, fmt.Errorf("%w: name cannot be empty", ErrValidation)
		}
		updates["name"] = name
	}
	if request.APIKey != nil {
		// 空串表示不修改，保留原 key。
		if apiKey := strings.TrimSpace(*request.APIKey); apiKey != "" {
			updates["api_key"] = apiKey
		}
	}
	if request.Endpoint != nil {
		endpoint, err := normalizeEndpoint(*request.Endpoint)
		if err != nil {
			return entities.ZenMuxCredential{}, err
		}
		updates["endpoint"] = endpoint
	}
	if request.ProxyURL != nil {
		proxyURL, err := normalizeProxyURL(*request.ProxyURL)
		if err != nil {
			return entities.ZenMuxCredential{}, err
		}
		updates["proxy_url"] = proxyURL
	}
	if request.AuthTypeSet && (!request.AuthIndexSet || request.AuthIndex == nil) {
		return entities.ZenMuxCredential{}, fmt.Errorf("%w: auth_type requires a non-null auth_index", ErrValidation)
	}
	if request.AuthIndexSet {
		if request.AuthIndex == nil {
			// 显式 null：解除绑定，同时清空绑定类型。
			updates["auth_index"] = nil
			updates["bound_auth_type"] = nil
		} else {
			authIndex, authType, err := s.resolveBinding(ctx, request.AuthIndex, request.AuthType)
			if err != nil {
				return entities.ZenMuxCredential{}, err
			}
			updates["auth_index"] = *authIndex
			updates["bound_auth_type"] = *authType
		}
	}
	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(&row).Updates(updates).Error; err != nil {
			return entities.ZenMuxCredential{}, err
		}
	}
	// UPDATE 由 dbresolver 自动路由 writer；结果回读固定到同一物理池，避免读到旧值。
	if err := s.db.Clauses(dbresolver.Write).WithContext(ctx).First(&row, id).Error; err != nil {
		return entities.ZenMuxCredential{}, err
	}
	return row, nil
}

func (s *service) Delete(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: invalid id", ErrValidation)
	}
	result := s.db.WithContext(ctx).Delete(&entities.ZenMuxCredential{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *service) Verify(ctx context.Context, id int64) (entities.ZenMuxCredential, error) {
	if id <= 0 {
		return entities.ZenMuxCredential{}, fmt.Errorf("%w: invalid id", ErrValidation)
	}
	var row entities.ZenMuxCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return entities.ZenMuxCredential{}, err
	}

	client, err := verifyClient(s.httpClient, row.ProxyURL)
	if err != nil {
		return entities.ZenMuxCredential{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	result, verifyErr := verifyBalance(ctx, client, row.Endpoint, row.APIKey)
	now := time.Now()
	row.CheckedAt = &now
	row.CheckError = ""
	if verifyErr != nil {
		row.CheckStatus = entities.ZenMuxCredentialCheckStatusFailed
		row.TotalBalance = nil
		row.TopUpCredits = nil
		row.BonusCredits = nil
		row.SubscriptionJSON = nil
		row.CheckError = verifyErr.Error()
	} else {
		row.CheckStatus = entities.ZenMuxCredentialCheckStatusSuccess
		row.TotalBalance = &result.TotalBalance
		row.TopUpCredits = &result.TopUpCredits
		row.BonusCredits = &result.BonusCredits
		// 订阅详情 best-effort：任何失败都不影响 balance 验证结果，订阅降级为 NULL。
		if subscription, subscriptionErr := fetchSubscription(ctx, client, row.Endpoint, row.APIKey); subscriptionErr == nil {
			normalized, err := json.Marshal(subscription)
			if err == nil {
				normalizedString := string(normalized)
				row.SubscriptionJSON = &normalizedString
			} else {
				row.SubscriptionJSON = nil
			}
		} else {
			row.SubscriptionJSON = nil
		}
	}
	if err := s.db.WithContext(ctx).Save(&row).Error; err != nil {
		return entities.ZenMuxCredential{}, err
	}
	return row, nil
}

// StatsByAuthIndexes 按 identity+auth_type 成对批量读取 usage identity 的本地统计；
// 不存在的绑定不出现在返回 map 中。
func (s *service) StatsByAuthIndexes(ctx context.Context, bindings []AuthBinding) (map[AuthBinding]CredentialStats, error) {
	result := make(map[AuthBinding]CredentialStats)
	clean := make([]AuthBinding, 0, len(bindings))
	seen := make(map[AuthBinding]struct{}, len(bindings))
	for _, binding := range bindings {
		binding.AuthIndex = strings.TrimSpace(binding.AuthIndex)
		if binding.AuthIndex == "" {
			continue
		}
		if _, ok := seen[binding]; ok {
			continue
		}
		seen[binding] = struct{}{}
		clean = append(clean, binding)
	}
	if len(clean) == 0 {
		return result, nil
	}

	grouped := s.db.WithContext(ctx).Where("identity = ? AND auth_type = ?", clean[0].AuthIndex, clean[0].AuthType)
	for _, binding := range clean[1:] {
		grouped = grouped.Or("identity = ? AND auth_type = ?", binding.AuthIndex, binding.AuthType)
	}
	var rows []entities.UsageIdentity
	if err := s.db.WithContext(ctx).
		Select("identity, auth_type, total_requests, success_count, failure_count, input_tokens, cache_read_tokens, total_tokens").
		Where("is_deleted = ?", false).
		Where(grouped).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		binding := AuthBinding{AuthIndex: row.Identity, AuthType: row.AuthType}
		result[binding] = buildCredentialStats(row)
	}
	return result, nil
}

func buildCredentialStats(row entities.UsageIdentity) CredentialStats {
	stats := CredentialStats{
		TotalRequests:   row.TotalRequests,
		SuccessCount:    row.SuccessCount,
		FailureCount:    row.FailureCount,
		TotalTokens:     row.TotalTokens,
		CacheReadTokens: row.CacheReadTokens,
	}
	if row.TotalRequests > 0 {
		stats.SuccessRate = float64(row.SuccessCount) / float64(row.TotalRequests)
	}
	// cache_read_rate 复用现有 Auth Files 列表口径：cache_read_tokens / input_tokens。
	if row.InputTokens > 0 {
		stats.CacheReadRate = float64(row.CacheReadTokens) / float64(row.InputTokens)
	}
	return stats
}

// resolveBinding 校验 auth_index/auth_type 成对关系并验证绑定身份存在。
// auth_index 为 nil 时返回未绑定；auth_type 缺省按 Auth File（1）处理。
func (s *service) resolveBinding(ctx context.Context, authIndex *string, authType *entities.UsageIdentityAuthType) (*string, *int, error) {
	if authIndex == nil {
		if authType != nil {
			return nil, nil, fmt.Errorf("%w: auth_type requires auth_index", ErrValidation)
		}
		return nil, nil, nil
	}
	trimmed := strings.TrimSpace(*authIndex)
	if trimmed == "" {
		return nil, nil, fmt.Errorf("%w: auth_index cannot be empty", ErrValidation)
	}
	resolvedType := entities.UsageIdentityAuthTypeAuthFile
	if authType != nil {
		if *authType != entities.UsageIdentityAuthTypeAuthFile && *authType != entities.UsageIdentityAuthTypeAIProvider {
			return nil, nil, fmt.Errorf("%w: invalid auth_type", ErrValidation)
		}
		resolvedType = *authType
	}
	exists, err := s.authIdentityExists(ctx, trimmed, resolvedType)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, fmt.Errorf("%w: auth identity not found", ErrValidation)
	}
	resolvedTypeValue := int(resolvedType)
	return &trimmed, &resolvedTypeValue, nil
}

func (s *service) authIdentityExists(ctx context.Context, authIndex string, authType entities.UsageIdentityAuthType) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&entities.UsageIdentity{}).
		Where("identity = ? AND auth_type = ? AND is_deleted = ?", authIndex, authType, false).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// normalizeEndpoint 为空时回退默认端点，并校验必须是 http/https URL。
func normalizeEndpoint(endpoint string) (string, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		trimmed = entities.DefaultZenMuxEndpoint
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: endpoint must be a valid URL", ErrValidation)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: endpoint must be an http or https URL", ErrValidation)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("%w: endpoint must be an http or https URL", ErrValidation)
	}
	return trimmed, nil
}

// normalizeProxyURL 校验可选代理 URL；空串表示不使用显式代理。
func normalizeProxyURL(proxyURL string) (string, error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: proxy_url must be a valid URL", ErrValidation)
	}
	switch parsed.Scheme {
	case "http", "https", "socks5":
	default:
		return "", fmt.Errorf("%w: proxy_url scheme must be http, https or socks5", ErrValidation)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("%w: proxy_url must be a valid URL", ErrValidation)
	}
	return trimmed, nil
}
