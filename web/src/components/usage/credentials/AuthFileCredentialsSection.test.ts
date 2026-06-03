import { describe, expect, it, vi } from 'vitest'
import { formatInspectionCompletedAt, formatInspectionProgressPercent, formatQuotaResetDuration, formatQuotaResetLabel, formatQuotaWindowUsageAriaLabel, inspectionIndicatorTone, isInspectionStartDisabled } from './AuthFileCredentialsSection'

const formatLocalResetTime = (resetAt: string) => {
  const resetTime = new Date(resetAt)
  const month = String(resetTime.getMonth() + 1).padStart(2, '0')
  const day = String(resetTime.getDate()).padStart(2, '0')
  const hour = String(resetTime.getHours()).padStart(2, '0')
  const minute = String(resetTime.getMinutes()).padStart(2, '0')
  return `${month}/${day} ${hour}:${minute}`
}

describe('AuthFileCredentialsSection quota reset formatting', () => {
  it('formats reset labels with days when remaining time exceeds 24 hours', () => {
    vi.setSystemTime(new Date('2026-05-10T10:00:00Z'))
    try {
      const resetAt = '2026-05-12T10:15:00Z'
      expect(formatQuotaResetLabel(resetAt)).toBe(formatLocalResetTime(resetAt))
      expect(formatQuotaResetDuration(resetAt)).toBe('2d0h15m')
    } finally {
      vi.useRealTimers()
    }
  })

  it('formats reset labels without days when remaining time is under 24 hours', () => {
    vi.setSystemTime(new Date('2026-05-10T10:00:00Z'))
    try {
      const resetAt = '2026-05-10T14:15:00Z'
      expect(formatQuotaResetLabel(resetAt)).toBe(formatLocalResetTime(resetAt))
      expect(formatQuotaResetDuration(resetAt)).toBe('4h15m')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('AuthFileCredentialsSection quota window usage accessibility', () => {
  it('labels token and cost metrics for assistive technology', () => {
    const t = (key: string, options?: Record<string, string>) => `${key}:${options?.tokens}:${options?.cost}`

    expect(formatQuotaWindowUsageAriaLabel(t, { tokens: '1.2M', cost: '$0.42' })).toBe('usage_stats.credentials_quota_window_usage_aria:1.2M:$0.42')
  })
})

describe('AuthFileCredentialsSection inspection controls', () => {
  it('calculates progress from cached quota results and total active auth files', () => {
    expect(formatInspectionProgressPercent({ total: 5, cached: 2 })).toBe(40)
    expect(formatInspectionProgressPercent({ total: 0, cached: 2 })).toBe(0)
    expect(formatInspectionProgressPercent({ total: 5, cached: 9 })).toBe(100)
  })

  it('disables manual inspection while auto refresh or an inspection round is active', () => {
    expect(isInspectionStartDisabled({ quotaAutoRefreshEnabled: true, starting: false, total: 5, running: false })).toBe(true)
    expect(isInspectionStartDisabled({ quotaAutoRefreshEnabled: false, starting: true, total: 5, running: false })).toBe(true)
    expect(isInspectionStartDisabled({ quotaAutoRefreshEnabled: false, starting: false, total: 5, running: true })).toBe(true)
    expect(isInspectionStartDisabled({ quotaAutoRefreshEnabled: false, starting: false, total: 0, running: false })).toBe(true)
    expect(isInspectionStartDisabled({ quotaAutoRefreshEnabled: false, starting: false, total: 5, running: false })).toBe(false)
  })

  it('uses running and completed status dots for the Auth Files inspection button', () => {
    expect(inspectionIndicatorTone({ running: true, completed: false })).toBe('running')
    expect(inspectionIndicatorTone({ running: false, completed: true })).toBe('completed')
    expect(inspectionIndicatorTone(null)).toBe('idle')
  })

  it('formats the cached inspection completion time', () => {
    expect(formatInspectionCompletedAt(undefined)).toBe('')
    expect(formatInspectionCompletedAt('invalid')).toBe('')
    expect(formatInspectionCompletedAt('2026-06-03T10:30:00Z')).toContain('2026')
  })
})
