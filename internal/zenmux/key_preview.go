package zenmux

import "strings"

// APIKeyPreview 生成安全的 api_key_preview：长度 >9 时保留前 5 位与后 4 位，中间用 **** 遮挡；
// 短 key 整体遮挡。绝不返回完整 api_key。
func APIKeyPreview(apiKey string) string {
	runes := []rune(strings.TrimSpace(apiKey))
	if len(runes) <= 9 {
		return "****"
	}
	return string(runes[:5]) + "****" + string(runes[len(runes)-4:])
}
