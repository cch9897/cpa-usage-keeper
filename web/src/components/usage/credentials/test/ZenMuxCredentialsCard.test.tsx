import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { ZenMuxCredentialsCard, ZenMuxCredentialForm } from '../ZenMuxCredentialsCard'
import type { UsageIdentity, ZenMuxCredential } from '@/lib/types'

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => undefined },
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string | number>) => (
      params && 'name' in params ? `${key}:${String(params.name)}` : key
    ),
  }),
}))

vi.mock('@/lib/api', () => ({
  ApiError: class ApiError extends Error {
    status?: number
  },
  fetchZenMuxCredentials: vi.fn(),
  createZenMuxCredential: vi.fn(),
  updateZenMuxCredential: vi.fn(),
  deleteZenMuxCredential: vi.fn(),
  verifyZenMuxCredential: vi.fn(),
}))

const authFileRows: UsageIdentity[] = [{
  id: '1',
  name: 'Auth Xyz',
  displayName: 'Auth Xyz',
  identity: 'auth-xyz',
  auth_type: 1,
} as UsageIdentity]

const verifiedCredential: ZenMuxCredential = {
  id: '1',
  name: '主账号',
  api_key_preview: 'sk-m-****abcd',
  endpoint: 'https://zenmux.ai/api/v1/management/payg/balance',
  auth_index: 'auth-xyz',
  check: {
    status: 'success',
    checked_at: '2026-08-29T10:00:00Z',
    total_balance: 12.34,
    top_up_credits: 10,
    bonus_credits: 2.34,
    error: null,
  },
  stats: {
    total_requests: 100,
    success_count: 98,
    failure_count: 2,
    success_rate: 0.98,
    total_tokens: 123456,
    cache_read_tokens: 30000,
    cache_read_rate: 0.24,
  },
  created_at: '2026-08-01T00:00:00Z',
  updated_at: '2026-08-29T10:00:00Z',
}

describe('ZenMuxCredentialsCard', () => {
  it('renders the credential list with balance and Keeper statistics', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard authFileRows={authFileRows} initialItems={[verifiedCredential]} />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_title')
    expect(html).toContain('usage_stats.credentials_zenmux_subtitle')
    expect(html).toContain('usage_stats.credentials_count')
    expect(html).toContain('usage_stats.credentials_zenmux_add')
    expect(html).toContain('主账号')
    expect(html).toContain('sk-m-****abcd')
    expect(html).toContain('https://zenmux.ai/api/v1/management/payg/balance')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_identity')
    expect(html).toContain('Auth Xyz')
    expect(html).toContain('$12.34')
    expect(html).toContain('$10.00')
    expect(html).toContain('$2.34')
    expect(html).toContain('usage_stats.credentials_zenmux_balance_topup')
    expect(html).toContain('usage_stats.credentials_zenmux_balance_bonus')
    expect(html).toContain('98.00%')
    expect(html).toContain('123.46K')
    expect(html).toContain('24.00%')
    expect(html).toContain('usage_stats.success_rate')
    expect(html).toContain('usage_stats.total_tokens')
    expect(html).toContain('usage_stats.cache_rate')
    expect(html).toContain('usage_stats.credentials_zenmux_last_verified')
    expect(html).toContain('usage_stats.credentials_zenmux_verify')
    expect(html).toContain('common.edit')
    expect(html).toContain('common.delete')
  })

  it('renders the empty state when there are no credentials', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard authFileRows={[]} initialItems={[]} />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_empty')
    expect(html).not.toContain('sk-m-****abcd')
  })

  it('shows the failed verification error summary and dashes for unbound Keeper statistics', () => {
    const failedCredential: ZenMuxCredential = {
      id: '2',
      name: '备用账号',
      api_key_preview: 'sk-m-****wxyz',
      endpoint: 'https://zenmux.ai/api/v1/management/payg/balance',
      auth_index: null,
      check: {
        status: 'failed',
        checked_at: '2026-08-29T11:00:00Z',
        total_balance: null,
        top_up_credits: null,
        bonus_credits: null,
        error: 'HTTP 401: invalid management key',
      },
      stats: null,
      created_at: '2026-08-02T00:00:00Z',
      updated_at: '2026-08-29T11:00:00Z',
    }

    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard authFileRows={[]} initialItems={[failedCredential]} />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_verify_failed')
    expect(html).toContain('HTTP 401: invalid management key')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_none')
    expect(html).not.toContain('usage_stats.credentials_zenmux_never_verified')
    // 未绑定身份时余额与 Keeper 统计都显示占位符 '-'
    expect(html.match(/>-</g)?.length ?? 0).toBeGreaterThanOrEqual(4)
    expect(html).not.toContain('usage_stats.credentials_zenmux_never_verified')
  })

  it('renders every modal form field with the edit-mode API key placeholder', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialForm
        mode="edit"
        name="主账号"
        apiKey=""
        endpoint="https://zenmux.ai/api/v1/management/payg/balance"
        authIndex="auth-xyz"
        apiKeyPreview="sk-m-****abcd"
        authFileOptions={[{ value: 'auth-xyz', label: 'Auth Xyz' }]}
        onChange={() => undefined}
      />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_name')
    expect(html).toContain('usage_stats.credentials_zenmux_api_key')
    expect(html).toContain('usage_stats.credentials_zenmux_endpoint')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_identity')
    expect(html).toContain('sk-m-****abcd')
    expect(html).toContain('usage_stats.credentials_zenmux_api_key_keep')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_identity')
    expect(html).toContain('Auth Xyz')
  })

  it('uses the API key placeholder hint in create mode', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialForm
        mode="create"
        name=""
        apiKey=""
        endpoint="https://zenmux.ai/api/v1/management/payg/balance"
        authIndex=""
        authFileOptions={[]}
        onChange={() => undefined}
      />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_api_key_placeholder')
    expect(html).not.toContain('usage_stats.credentials_zenmux_api_key_keep')
  })
})
