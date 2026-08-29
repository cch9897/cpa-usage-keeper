package entities

import "time"

// DefaultZenMuxEndpoint 是创建/更新 ZenMux 凭证时未显式提供端点所用的默认验证端点。
const DefaultZenMuxEndpoint = "https://zenmux.ai/api/v1/management/payg/balance"

// ZenMuxCredentialCheckStatus 表示一次余额验证的结果状态。
const (
	ZenMuxCredentialCheckStatusSuccess = "success"
	ZenMuxCredentialCheckStatusFailed  = "failed"
)

// ZenMuxCredential 保存手动添加的 ZenMux Management API Key 与最近一次余额验证结果。
// 完整 APIKey 仅供后端验证请求内部使用，任何 API 响应与日志都不得直接返回。
type ZenMuxCredential struct {
	ID     int64  `gorm:"primaryKey"`
	Name   string `gorm:"not null"`
	APIKey string `gorm:"not null;column:api_key"`
	// Endpoint 是余额验证请求地址；创建/更新未提供时回退到 DefaultZenMuxEndpoint。
	Endpoint string `gorm:"not null"`
	// ProxyURL 是验证请求使用的可选专用代理；空串表示走环境变量代理（ProxyFromEnvironment）。
	ProxyURL string `gorm:"not null;default:'';column:proxy_url"`
	// AuthIndex 绑定的 Keeper usage identity；nil 表示未绑定。
	AuthIndex *string
	// BoundAuthType 是绑定身份的 auth_type（1=Auth File，2=AI Provider）；nil 表示旧数据，按 1 处理。
	BoundAuthType *int `gorm:"column:bound_auth_type"`
	// CheckStatus 为空串表示从未验证，否则为 success/failed。
	CheckStatus string     `gorm:"column:check_status"`
	CheckedAt   *time.Time `gorm:"serializer:storageTime;column:checked_at"`
	// TotalBalance/TopUpCredits/BonusCredits 仅在验证成功后写入，失败/未验证为 nil。
	TotalBalance *float64 `gorm:"column:total_balance"`
	TopUpCredits *float64 `gorm:"column:top_up_credits"`
	BonusCredits *float64 `gorm:"column:bonus_credits"`
	// CheckError 保存最近一次失败原因（截断、绝不包含 api_key）。
	CheckError string    `gorm:"column:check_error"`
	CreatedAt  time.Time `gorm:"serializer:storageTime;not null;column:created_at"`
	UpdatedAt  time.Time `gorm:"serializer:storageTime;not null;column:updated_at"`
}

func (ZenMuxCredential) TableName() string {
	return "zenmux_credentials"
}
