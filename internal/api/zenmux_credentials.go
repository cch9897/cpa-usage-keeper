package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"
	"cpa-usage-keeper/internal/zenmux"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	zenMuxAuthTypeAuthFile   = "auth-file"
	zenMuxAuthTypeAIProvider = "ai-provider"
)

// zenMuxCredentialProvider 是 ZenMux 凭证路由依赖的最小接口，测试可用 stub 替换。
type zenMuxCredentialProvider interface {
	List(ctx context.Context) ([]entities.ZenMuxCredential, error)
	Create(ctx context.Context, request zenmux.CreateRequest) (entities.ZenMuxCredential, error)
	Update(ctx context.Context, id int64, request zenmux.UpdateRequest) (entities.ZenMuxCredential, error)
	Delete(ctx context.Context, id int64) error
	Verify(ctx context.Context, id int64) (entities.ZenMuxCredential, error)
	StatsByAuthIndexes(ctx context.Context, bindings []zenmux.AuthBinding) (map[zenmux.AuthBinding]zenmux.CredentialStats, error)
}

type zenMuxCredentialCreateRequest struct {
	Name      string  `json:"name"`
	APIKey    string  `json:"api_key"`
	Endpoint  string  `json:"endpoint"`
	ProxyURL  string  `json:"proxy_url"`
	AuthIndex *string `json:"auth_index"`
	AuthType  *string `json:"auth_type"`
}

type zenMuxCredentialUpdateRequest struct {
	Name      *string         `json:"name"`
	APIKey    *string         `json:"api_key"`
	Endpoint  *string         `json:"endpoint"`
	ProxyURL  *string         `json:"proxy_url"`
	AuthIndex json.RawMessage `json:"auth_index"`
	AuthType  json.RawMessage `json:"auth_type"`
}

type zenMuxCredentialCheck struct {
	Status       string   `json:"status"`
	CheckedAt    *string  `json:"checked_at"`
	TotalBalance *float64 `json:"total_balance"`
	TopUpCredits *float64 `json:"top_up_credits"`
	BonusCredits *float64 `json:"bonus_credits"`
	Error        *string  `json:"error"`
}

type zenMuxCredentialStats struct {
	TotalRequests   int64   `json:"total_requests"`
	SuccessCount    int64   `json:"success_count"`
	FailureCount    int64   `json:"failure_count"`
	SuccessRate     float64 `json:"success_rate"`
	TotalTokens     int64   `json:"total_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	CacheReadRate   float64 `json:"cache_read_rate"`
}

type zenMuxCredentialResponse struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	APIKeyPreview string                 `json:"api_key_preview"`
	Endpoint      string                 `json:"endpoint"`
	ProxyURL      string                 `json:"proxy_url"`
	AuthIndex     *string                `json:"auth_index"`
	AuthType      *string                `json:"auth_type"`
	Check         zenMuxCredentialCheck  `json:"check"`
	Stats         *zenMuxCredentialStats `json:"stats"`
	CreatedAt     string                 `json:"created_at"`
	UpdatedAt     string                 `json:"updated_at"`
}

type zenMuxCredentialListResponse struct {
	Items []zenMuxCredentialResponse `json:"items"`
}

func registerZenMuxCredentialRoutes(router gin.IRoutes, provider zenMuxCredentialProvider) {
	router.GET("/zenmux/credentials", func(c *gin.Context) {
		rows, err := listZenMuxCredentialRows(c, provider)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, zenMuxCredentialListResponse{Items: rows})
	})

	router.POST("/zenmux/credentials", func(c *gin.Context) {
		if provider == nil {
			writeInternalError(c, "zenmux credential provider is not configured", nil)
			return
		}
		var request zenMuxCredentialCreateRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		authType, ok := parseZenMuxCreateAuthType(request.AuthType)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auth_type"})
			return
		}
		row, err := provider.Create(c.Request.Context(), zenmux.CreateRequest{
			Name:      request.Name,
			APIKey:    request.APIKey,
			Endpoint:  request.Endpoint,
			ProxyURL:  request.ProxyURL,
			AuthIndex: request.AuthIndex,
			AuthType:  authType,
		})
		if err != nil {
			writeZenMuxCredentialError(c, "create zenmux credential failed", err)
			return
		}
		response, err := zenMuxCredentialResponseForRow(c, provider, row)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, response)
	})

	router.PUT("/zenmux/credentials/:id", func(c *gin.Context) {
		if provider == nil {
			writeInternalError(c, "zenmux credential provider is not configured", nil)
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid zenmux credential id"})
			return
		}
		var request zenMuxCredentialUpdateRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		updateRequest, ok := toZenMuxCredentialUpdateRequest(request)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		row, err := provider.Update(c.Request.Context(), id, updateRequest)
		if err != nil {
			writeZenMuxCredentialError(c, "update zenmux credential failed", err)
			return
		}
		response, err := zenMuxCredentialResponseForRow(c, provider, row)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, response)
	})

	router.DELETE("/zenmux/credentials/:id", func(c *gin.Context) {
		if provider == nil {
			writeInternalError(c, "zenmux credential provider is not configured", nil)
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid zenmux credential id"})
			return
		}
		if err := provider.Delete(c.Request.Context(), id); err != nil {
			writeZenMuxCredentialError(c, "delete zenmux credential failed", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	router.POST("/zenmux/credentials/:id/verify", func(c *gin.Context) {
		if provider == nil {
			writeInternalError(c, "zenmux credential provider is not configured", nil)
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid zenmux credential id"})
			return
		}
		row, err := provider.Verify(c.Request.Context(), id)
		if err != nil {
			writeZenMuxCredentialError(c, "verify zenmux credential failed", err)
			return
		}
		response, err := zenMuxCredentialResponseForRow(c, provider, row)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, response)
	})
}

func listZenMuxCredentialRows(c *gin.Context, provider zenMuxCredentialProvider) ([]zenMuxCredentialResponse, error) {
	if provider == nil {
		return []zenMuxCredentialResponse{}, nil
	}
	rows, err := provider.List(c.Request.Context())
	if err != nil {
		writeInternalError(c, "list zenmux credentials failed", err)
		return nil, err
	}
	bindings := make([]zenmux.AuthBinding, 0, len(rows))
	for _, row := range rows {
		if row.AuthIndex != nil {
			bindings = append(bindings, zenMuxCredentialAuthBinding(row))
		}
	}
	statsByBinding, err := provider.StatsByAuthIndexes(c.Request.Context(), bindings)
	if err != nil {
		writeInternalError(c, "load zenmux credential stats failed", err)
		return nil, err
	}
	response := make([]zenMuxCredentialResponse, 0, len(rows))
	for _, row := range rows {
		var stats *zenMuxCredentialStats
		if row.AuthIndex != nil {
			if item, ok := statsByBinding[zenMuxCredentialAuthBinding(row)]; ok {
				stats = toZenMuxCredentialStats(item)
			}
		}
		response = append(response, toZenMuxCredentialResponse(row, stats))
	}
	return response, nil
}

// zenMuxCredentialResponseForRow 构建单个凭证响应；绑定 auth_index 时附带本地统计。
func zenMuxCredentialResponseForRow(c *gin.Context, provider zenMuxCredentialProvider, row entities.ZenMuxCredential) (zenMuxCredentialResponse, error) {
	var stats *zenMuxCredentialStats
	if row.AuthIndex != nil {
		statsByBinding, err := provider.StatsByAuthIndexes(c.Request.Context(), []zenmux.AuthBinding{zenMuxCredentialAuthBinding(row)})
		if err != nil {
			writeInternalError(c, "load zenmux credential stats failed", err)
			return zenMuxCredentialResponse{}, err
		}
		if item, ok := statsByBinding[zenMuxCredentialAuthBinding(row)]; ok {
			stats = toZenMuxCredentialStats(item)
		}
	}
	return toZenMuxCredentialResponse(row, stats), nil
}

// zenMuxCredentialAuthBinding 从凭证行推导统计查询用的身份绑定；旧数据 bound_auth_type 为 nil 时按 1 处理。
func zenMuxCredentialAuthBinding(row entities.ZenMuxCredential) zenmux.AuthBinding {
	authType := entities.UsageIdentityAuthTypeAuthFile
	if row.BoundAuthType != nil {
		authType = entities.UsageIdentityAuthType(*row.BoundAuthType)
	}
	return zenmux.AuthBinding{AuthIndex: *row.AuthIndex, AuthType: authType}
}

// parseZenMuxCreateAuthType 解析创建请求的 auth_type；未提供时为 nil（服务端按 Auth File 兜底）。
func parseZenMuxCreateAuthType(value *string) (*entities.UsageIdentityAuthType, bool) {
	if value == nil {
		return nil, true
	}
	authType, err := parseZenMuxAuthType(*value)
	if err != nil {
		return nil, false
	}
	return &authType, true
}

// parseZenMuxAuthType 把 wire 上的 "auth-file"/"ai-provider" 映射为实体类型。
func parseZenMuxAuthType(value string) (entities.UsageIdentityAuthType, error) {
	switch strings.TrimSpace(value) {
	case zenMuxAuthTypeAuthFile:
		return entities.UsageIdentityAuthTypeAuthFile, nil
	case zenMuxAuthTypeAIProvider:
		return entities.UsageIdentityAuthTypeAIProvider, nil
	default:
		return 0, errors.New("invalid auth_type")
	}
}

// toZenMuxCredentialUpdateRequest 解析 PUT body；auth_index 显式 null 表示解除绑定，
// 未提供时保持原值；auth_type 缺省或 null 视为未提供。
func toZenMuxCredentialUpdateRequest(request zenMuxCredentialUpdateRequest) (zenmux.UpdateRequest, bool) {
	result := zenmux.UpdateRequest{
		Name:     request.Name,
		APIKey:   request.APIKey,
		Endpoint: request.Endpoint,
		ProxyURL: request.ProxyURL,
	}
	if len(request.AuthIndex) == 0 || bytes.Equal(bytes.TrimSpace(request.AuthIndex), []byte("null")) {
		result.AuthIndexSet = len(request.AuthIndex) > 0
	} else {
		var authIndex *string
		if err := json.Unmarshal(request.AuthIndex, &authIndex); err != nil {
			return zenmux.UpdateRequest{}, false
		}
		result.AuthIndex = authIndex
		result.AuthIndexSet = true
	}
	if len(request.AuthType) == 0 || bytes.Equal(bytes.TrimSpace(request.AuthType), []byte("null")) {
		return result, true
	}
	var authTypeValue *string
	if err := json.Unmarshal(request.AuthType, &authTypeValue); err != nil {
		return zenmux.UpdateRequest{}, false
	}
	if authTypeValue == nil {
		return result, true
	}
	authType, err := parseZenMuxAuthType(*authTypeValue)
	if err != nil {
		return zenmux.UpdateRequest{}, false
	}
	result.AuthType = &authType
	result.AuthTypeSet = true
	return result, true
}

func toZenMuxCredentialResponse(row entities.ZenMuxCredential, stats *zenMuxCredentialStats) zenMuxCredentialResponse {
	return zenMuxCredentialResponse{
		ID:            strconv.FormatInt(row.ID, 10),
		Name:          row.Name,
		APIKeyPreview: zenmux.APIKeyPreview(row.APIKey),
		Endpoint:      row.Endpoint,
		ProxyURL:      row.ProxyURL,
		AuthIndex:     row.AuthIndex,
		AuthType:      zenMuxCredentialAuthType(row),
		Check:         toZenMuxCredentialCheck(row),
		Stats:         stats,
		CreatedAt:     timeutil.FormatStorageTime(row.CreatedAt),
		UpdatedAt:     timeutil.FormatStorageTime(row.UpdatedAt),
	}
}

// zenMuxCredentialAuthType 映射绑定类型：未绑定为 nil，否则 1→"auth-file"、2→"ai-provider"。
func zenMuxCredentialAuthType(row entities.ZenMuxCredential) *string {
	if row.AuthIndex == nil {
		return nil
	}
	authType := zenMuxAuthTypeAuthFile
	if row.BoundAuthType != nil && *row.BoundAuthType == int(entities.UsageIdentityAuthTypeAIProvider) {
		authType = zenMuxAuthTypeAIProvider
	}
	return &authType
}

func toZenMuxCredentialCheck(row entities.ZenMuxCredential) zenMuxCredentialCheck {
	status := row.CheckStatus
	if status == "" {
		status = "never"
	}
	var checkedAt *string
	if row.CheckedAt != nil {
		value := timeutil.FormatStorageTime(*row.CheckedAt)
		checkedAt = &value
	}
	var checkError *string
	if row.CheckError != "" {
		checkError = &row.CheckError
	}
	return zenMuxCredentialCheck{
		Status:       status,
		CheckedAt:    checkedAt,
		TotalBalance: row.TotalBalance,
		TopUpCredits: row.TopUpCredits,
		BonusCredits: row.BonusCredits,
		Error:        checkError,
	}
}

func toZenMuxCredentialStats(stats zenmux.CredentialStats) *zenMuxCredentialStats {
	return &zenMuxCredentialStats{
		TotalRequests:   stats.TotalRequests,
		SuccessCount:    stats.SuccessCount,
		FailureCount:    stats.FailureCount,
		SuccessRate:     stats.SuccessRate,
		TotalTokens:     stats.TotalTokens,
		CacheReadTokens: stats.CacheReadTokens,
		CacheReadRate:   stats.CacheReadRate,
	}
}

func writeZenMuxCredentialError(c *gin.Context, message string, err error) {
	switch {
	case errors.Is(err, zenmux.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "zenmux credential not found"})
	default:
		writeInternalError(c, message, err)
	}
}
