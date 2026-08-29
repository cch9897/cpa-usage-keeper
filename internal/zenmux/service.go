package zenmux

import (
	"context"
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
	AuthIndex *string
}

// UpdateRequest 是更新 ZenMux 管理凭证的输入；指针字段为 nil 表示不修改。
// AuthIndexSet 区分 auth_index 未提供（保持原值）与显式 null（解除绑定）。
type UpdateRequest struct {
	Name         *string
	APIKey       *string
	Endpoint     *string
	AuthIndex    *string
	AuthIndexSet bool
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

// Provider 是 ZenMux 管理凭证业务能力接口，供 API 层与测试 stub 使用。
type Provider interface {
	List(ctx context.Context) ([]entities.ZenMuxCredential, error)
	Create(ctx context.Context, request CreateRequest) (entities.ZenMuxCredential, error)
	Update(ctx context.Context, id int64, request UpdateRequest) (entities.ZenMuxCredential, error)
	Delete(ctx context.Context, id int64) error
	Verify(ctx context.Context, id int64) (entities.ZenMuxCredential, error)
	StatsByAuthIndexes(ctx context.Context, authIndexes []string) (map[string]CredentialStats, error)
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

	row := entities.ZenMuxCredential{
		Name:      name,
		APIKey:    apiKey,
		Endpoint:  endpoint,
		AuthIndex: normalizeAuthIndex(request.AuthIndex),
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
	if request.AuthIndexSet {
		if authIndex := normalizeAuthIndex(request.AuthIndex); authIndex == nil {
			updates["auth_index"] = nil
		} else {
			updates["auth_index"] = *authIndex
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

	result, verifyErr := verifyBalance(ctx, s.httpClient, row.Endpoint, row.APIKey)
	now := time.Now()
	row.CheckedAt = &now
	row.CheckError = ""
	if verifyErr != nil {
		row.CheckStatus = entities.ZenMuxCredentialCheckStatusFailed
		row.TotalBalance = nil
		row.TopUpCredits = nil
		row.BonusCredits = nil
		row.CheckError = verifyErr.Error()
	} else {
		row.CheckStatus = entities.ZenMuxCredentialCheckStatusSuccess
		row.TotalBalance = &result.TotalBalance
		row.TopUpCredits = &result.TopUpCredits
		row.BonusCredits = &result.BonusCredits
	}
	if err := s.db.WithContext(ctx).Save(&row).Error; err != nil {
		return entities.ZenMuxCredential{}, err
	}
	return row, nil
}

// StatsByAuthIndexes 批量读取绑定 auth_type=1（Auth File）usage identity 的本地统计；
// 不存在的 auth_index 不出现在返回 map 中。
func (s *service) StatsByAuthIndexes(ctx context.Context, authIndexes []string) (map[string]CredentialStats, error) {
	result := make(map[string]CredentialStats)
	clean := make([]string, 0, len(authIndexes))
	seen := make(map[string]struct{}, len(authIndexes))
	for _, authIndex := range authIndexes {
		trimmed := strings.TrimSpace(authIndex)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		clean = append(clean, trimmed)
	}
	if len(clean) == 0 {
		return result, nil
	}

	var rows []entities.UsageIdentity
	if err := s.db.WithContext(ctx).
		Select("identity, total_requests, success_count, failure_count, input_tokens, cache_read_tokens, total_tokens").
		Where("identity IN ? AND auth_type = ? AND is_deleted = ?", clean, entities.UsageIdentityAuthTypeAuthFile, false).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.Identity] = buildCredentialStats(row)
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

// normalizeAuthIndex 把空白 auth_index 规范化为 nil（未绑定）。
func normalizeAuthIndex(authIndex *string) *string {
	if authIndex == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*authIndex)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
