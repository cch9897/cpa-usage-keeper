package zenmux

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SubscriptionQuotaWindow 是单个限额窗口（5 小时 / 7 天）的规范化结构。
type SubscriptionQuotaWindow struct {
	UsagePercentage float64 `json:"usage_percentage"`
	UsedFlows       float64 `json:"used_flows"`
	RemainingFlows  float64 `json:"remaining_flows"`
	MaxFlows        float64 `json:"max_flows"`
	ResetsAt        *string `json:"resets_at"`
}

// SubscriptionMonthly 是月度限额的规范化结构。
type SubscriptionMonthly struct {
	MaxFlows    float64 `json:"max_flows"`
	MaxValueUSD float64 `json:"max_value_usd"`
}

// Subscription 是订阅/限额信息的规范化结构，也直接作为 DTO.subscription 的响应体。
type Subscription struct {
	PlanTier      string                   `json:"plan_tier"`
	PlanExpiresAt *string                  `json:"plan_expires_at"`
	AccountStatus string                   `json:"account_status"`
	Quota5Hour    *SubscriptionQuotaWindow `json:"quota_5_hour"`
	Quota7Day     *SubscriptionQuotaWindow `json:"quota_7_day"`
	QuotaMonthly  *SubscriptionMonthly     `json:"quota_monthly"`
}

// parseSubscriptionResponse 解析官方 subscription/detail 响应为规范化结构。
// 顶层或 data 子对象均可；解析失败返回错误，由调用方 best-effort 处理（存 NULL）。
func parseSubscriptionResponse(body []byte) (*Subscription, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	object := payload
	if data, ok := payload["data"]; ok {
		var dataObject map[string]json.RawMessage
		if err := json.Unmarshal(data, &dataObject); err == nil && dataObject != nil {
			object = dataObject
		}
	}

	subscription := &Subscription{}
	if plan, ok := object["plan"]; ok {
		var planObject map[string]json.RawMessage
		if err := json.Unmarshal(plan, &planObject); err == nil {
			subscription.PlanTier = jsonString(planObject["tier"])
			subscription.PlanExpiresAt = jsonTimeString(planObject["expires_at"])
		}
	}
	subscription.AccountStatus = jsonString(object["account_status"])
	subscription.Quota5Hour = parseSubscriptionQuotaWindow(object["quota_5_hour"])
	subscription.Quota7Day = parseSubscriptionQuotaWindow(object["quota_7_day"])
	if monthly, ok := object["quota_monthly"]; ok {
		var monthlyObject map[string]json.RawMessage
		if err := json.Unmarshal(monthly, &monthlyObject); err == nil {
			maxFlows, _ := lookupRawFloat(monthlyObject, "max_flows")
			maxValueUSD, _ := lookupRawFloat(monthlyObject, "max_value_usd")
			subscription.QuotaMonthly = &SubscriptionMonthly{MaxFlows: maxFlows, MaxValueUSD: maxValueUSD}
		}
	}
	if subscription.AccountStatus == "" && subscription.Quota5Hour == nil && subscription.Quota7Day == nil && subscription.QuotaMonthly == nil {
		return nil, fmt.Errorf("missing subscription data")
	}
	return subscription, nil
}

func parseSubscriptionQuotaWindow(raw json.RawMessage) *SubscriptionQuotaWindow {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	window := &SubscriptionQuotaWindow{}
	window.UsagePercentage, _ = lookupRawFloat(object, "usage_percentage")
	window.UsedFlows, _ = lookupRawFloat(object, "used_flows")
	window.RemainingFlows, _ = lookupRawFloat(object, "remaining_flows")
	window.MaxFlows, _ = lookupRawFloat(object, "max_flows")
	window.ResetsAt = jsonTimeString(object["resets_at"])
	return window
}

// lookupRawFloat 容忍 JSON number 与数字字符串。
func lookupRawFloat(object map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := object[key]
	if !ok {
		return 0, false
	}
	value, err := parseJSONFloat(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}
func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// jsonTimeString 返回时间字符串；能按 RFC3339 解析则规范化输出，否则保留原值。
func jsonTimeString(raw json.RawMessage) *string {
	value := jsonString(raw)
	if value == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		normalized := parsed.UTC().Format(time.RFC3339)
		return &normalized
	}
	return &value
}
