import { describe, expect, it } from 'vitest'
import i18n from '../index'

describe('zenmux inspection translations', () => {
  it('localizes the credentials inspection title in every language', () => {
    expect(i18n.getResource('en', 'translation', 'usage_stats.credentials_inspection_title')).toBe('Credentials Inspection')
    expect(i18n.getResource('zh', 'translation', 'usage_stats.credentials_inspection_title')).toBe('凭证巡检')
    expect(i18n.getResource('zh-TW', 'translation', 'usage_stats.credentials_inspection_title')).toBe('憑證巡檢')
  })

  it('localizes the ZenMux inspection group labels in every language', () => {
    const keys = [
      'credentials_inspection_zenmux_title',
      'credentials_inspection_zenmux_running',
      'credentials_inspection_zenmux_success',
      'credentials_inspection_zenmux_failed',
      'credentials_inspection_zenmux_unknown',
      'credentials_inspection_zenmux_empty_results',
    ]
    const expected = {
      en: ['ZenMux Credentials', 'Verifying ZenMux…', 'Verified', 'Failed', 'Never verified', 'No verification results yet.'],
      zh: ['ZenMux 凭证', 'ZenMux 验证中…', '验证成功', '验证失败', '未验证', '暂无验证结果。'],
      'zh-TW': ['ZenMux 憑證', 'ZenMux 驗證中…', '驗證成功', '驗證失敗', '尚未驗證', '尚無驗證結果。'],
    }
    for (const [language, values] of Object.entries(expected)) {
      expect(keys.map((key) => i18n.getResource(language, 'translation', `usage_stats.${key}`))).toEqual(values)
    }
  })
})