package zenmux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultVerifyTimeout 是单次余额验证请求的客户端超时。
const defaultVerifyTimeout = 15 * time.Second

// maxVerifyResponseBody 限制余额响应体读取长度，防止异常大响应拖垮验证。
const maxVerifyResponseBody = 1 << 20

// maxVerifyErrorBodyExcerpt 限制写入 check_error 的失败响应体摘录长度。
const maxVerifyErrorBodyExcerpt = 200

// balanceResult 是一次成功的余额验证解析结果。
type balanceResult struct {
	TotalBalance float64
	TopUpCredits float64
	BonusCredits float64
}

// verifyClient 按凭证代理配置构建验证用 HTTP client：proxyURL 非空时使用显式代理，
// 否则回退到 http.ProxyFromEnvironment；超时继承基础 client。
func verifyClient(base *http.Client, proxyURL string) (*http.Client, error) {
	timeout := defaultVerifyTimeout
	if base != nil && base.Timeout > 0 {
		timeout = base.Timeout
	}
	transport, err := buildVerifyTransport(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// buildVerifyTransport 构建验证请求传输层：proxyURL 非空时使用显式代理，否则走环境变量代理。
func buildVerifyTransport(proxyURL string) (*http.Transport, error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return &http.Transport{Proxy: http.ProxyFromEnvironment}, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	return &http.Transport{Proxy: http.ProxyURL(parsed)}, nil
}

// verifyBalance 向 endpoint 发起带 Bearer api_key 的 GET 请求并容忍解析余额字段。
// 非 2xx 返回 "HTTP <code>: <摘录>" 形式的错误；2xx 但解析不出余额返回说明性错误。
// 返回的 error 文本绝不包含 api_key。
func verifyBalance(ctx context.Context, client *http.Client, endpoint, apiKey string) (balanceResult, error) {
	statusCode, body, err := getWithBearer(ctx, client, endpoint, apiKey)
	if err != nil {
		return balanceResult{}, fmt.Errorf("balance request failed: %w", err)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		excerpt := strings.TrimSpace(string(body))
		if len(excerpt) > maxVerifyErrorBodyExcerpt {
			excerpt = excerpt[:maxVerifyErrorBodyExcerpt]
		}
		if excerpt == "" {
			return balanceResult{}, fmt.Errorf("HTTP %d", statusCode)
		}
		return balanceResult{}, fmt.Errorf("HTTP %d: %s", statusCode, excerpt)
	}

	result, err := parseBalanceResponse(body)
	if err != nil {
		return balanceResult{}, fmt.Errorf("unable to parse balance response: %w", err)
	}
	return result, nil
}

// getWithBearer 发起带 Bearer api_key 的 GET 请求，返回状态码与受限长度的响应体。
func getWithBearer(ctx context.Context, client *http.Client, endpoint, apiKey string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxVerifyResponseBody))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return response.StatusCode, body, nil
}

// subscriptionDetailURL 用配置端点的 scheme+host 构造订阅详情地址。
func subscriptionDetailURL(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	parsed.Path = "/api/v1/management/subscription/detail"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// fetchSubscription best-effort 拉取订阅详情；任何失败（网络/非 2xx/解析）都返回错误，
// 由调用方决定降级为 NULL，不影响 balance 验证结果。
func fetchSubscription(ctx context.Context, client *http.Client, endpoint, apiKey string) (*Subscription, error) {
	detailURL, err := subscriptionDetailURL(endpoint)
	if err != nil {
		return nil, err
	}
	statusCode, body, err := getWithBearer(ctx, client, detailURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("subscription request failed: %w", err)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("subscription request failed: HTTP %d", statusCode)
	}
	subscription, err := parseSubscriptionResponse(body)
	if err != nil {
		return nil, fmt.Errorf("unable to parse subscription response: %w", err)
	}
	return subscription, nil
}

// balanceFieldNames 是同一语义字段的容忍别名，按优先级顺序尝试。
var balanceFieldNames = map[string][]string{
	// total 优先官方 total_credits，再兼容历史别名。
	"total_balance":  {"total_credits", "totalCredits", "total_balance", "totalBalance", "balance"},
	"top_up_credits": {"top_up_credits", "topUpCredits", "topup_credits"},
	"bonus_credits":  {"bonus_credits", "bonusCredits"},
}

// parseBalanceResponse 从顶层或 data 子对象容忍解析三个余额字段；
// total_balance 缺失时报错，top_up/bonus 缺失时按 0 处理。
func parseBalanceResponse(body []byte) (balanceResult, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return balanceResult{}, fmt.Errorf("invalid json: %w", err)
	}

	object := payload
	if data, ok := payload["data"]; ok {
		var dataObject map[string]json.RawMessage
		if err := json.Unmarshal(data, &dataObject); err == nil && dataObject != nil {
			object = dataObject
		}
	}

	totalBalance, ok := lookupBalanceField(object, "total_balance")
	if !ok {
		return balanceResult{}, fmt.Errorf("missing total balance field")
	}
	topUpCredits, _ := lookupBalanceField(object, "top_up_credits")
	bonusCredits, _ := lookupBalanceField(object, "bonus_credits")
	return balanceResult{
		TotalBalance: totalBalance,
		TopUpCredits: topUpCredits,
		BonusCredits: bonusCredits,
	}, nil
}

// lookupBalanceField 按字段名别名依次尝试解析为数值；同时容忍 JSON 数字与数字字符串。
func lookupBalanceField(object map[string]json.RawMessage, field string) (float64, bool) {
	for _, name := range balanceFieldNames[field] {
		raw, ok := object[name]
		if !ok {
			continue
		}
		if value, err := parseJSONFloat(raw); err == nil {
			return value, true
		}
	}
	return 0, false
}

// parseJSONFloat 同时接受 JSON number 与带引号的数字字符串。
func parseJSONFloat(raw json.RawMessage) (float64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("empty value")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		return strconv.ParseFloat(strings.TrimSpace(text), 64)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}
