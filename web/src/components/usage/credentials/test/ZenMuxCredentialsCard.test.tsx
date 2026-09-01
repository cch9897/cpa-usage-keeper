// @vitest-environment happy-dom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { UsageIdentity, UsageIdentitiesResponse, ZenMuxCredential, ZenMuxCredentialsResponse } from '@/lib/types'
import { createZenMuxCredential, fetchUsageIdentities, fetchZenMuxCredentials } from '@/lib/api'
import { ZenMuxCredentialsCard, ZenMuxCredentialForm } from '../ZenMuxCredentialsCard'

globalThis.IS_REACT_ACT_ENVIRONMENT = true

// t 必须在渲染间保持稳定：组件把 t 放进 effect 依赖，重渲染时若 t 引用变化会重复拉取列表
const { mockT } = vi.hoisted(() => ({
  mockT: (key: string, params?: Record<string, string | number>) => (
    params && 'name' in params ? `${key}:${String(params.name)}` : key
  ),
}))

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => undefined },
  useTranslation: () => ({ t: mockT }),
}))

vi.mock('@/lib/api', () => ({
  ApiError: class ApiError extends Error {
    status?: number
  },
  fetchUsageIdentities: vi.fn(),
  fetchZenMuxCredentials: vi.fn(),
  createZenMuxCredential: vi.fn(),
  updateZenMuxCredential: vi.fn(),
  deleteZenMuxCredential: vi.fn(),
  verifyZenMuxCredential: vi.fn(),
}))

const deferred = <T,>() => {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

interface MountCardOptions {
  identities?: UsageIdentity[]
  items?: ZenMuxCredential[]
  identitiesError?: Error
}

// 挂载卡片并把两个拉取 effect 的 promise 在 act 内完成，避免异步续体在 act 外更新状态
const mountCardWithSettledFetches = async (root: Root, options: MountCardOptions = {}) => {
  const identitiesRequest = deferred<UsageIdentitiesResponse>()
  const itemsRequest = deferred<ZenMuxCredentialsResponse>()
  vi.mocked(fetchUsageIdentities).mockReturnValue(identitiesRequest.promise)
  vi.mocked(fetchZenMuxCredentials).mockReturnValue(itemsRequest.promise)
  await act(async () => {
    root.render(<ZenMuxCredentialsCard />)
  })
  await act(async () => {
    itemsRequest.resolve({ items: options.items ?? [] })
    await itemsRequest.promise
  })
  await act(async () => {
    if (options.identitiesError) {
      identitiesRequest.reject(options.identitiesError)
    } else {
      identitiesRequest.resolve({ identities: options.identities ?? [] })
    }
    await identitiesRequest.promise.catch(() => undefined)
  })
}

const authFileIdentity: UsageIdentity = {
  id: '1',
  name: 'Auth Xyz',
  displayName: 'Auth Xyz',
  identity: 'auth-xyz',
  auth_type: 1,
} as UsageIdentity

const aiProviderIdentity: UsageIdentity = {
  id: '2',
  name: 'AI Provider One',
  displayName: 'AI Provider One',
  identity: 'ai-1',
  auth_type: 2,
} as UsageIdentity

const verifiedCredential: ZenMuxCredential = {
  id: '1',
  name: '主账号',
  api_key_preview: 'sk-m-****abcd',
  endpoint: 'https://zenmux.ai/api/v1/management/payg/balance',
  proxy_url: '',
  auth_index: 'auth-xyz',
  auth_type: 'auth-file',
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
  subscription: null,
  created_at: '2026-08-01T00:00:00Z',
  updated_at: '2026-08-29T10:00:00Z',
}

const subscribedCredential: ZenMuxCredential = {
  ...verifiedCredential,
  id: '3',
  name: '订阅账号',
  api_key_preview: 'sk-m-****efgh',
  subscription: {
    plan_tier: 'ultra',
    plan_expires_at: '2026-12-31T00:00:00Z',
    account_status: 'healthy',
    quota_5_hour: {
      usage_percentage: 0.072,
      used_flows: 57.2,
      remaining_flows: 742.8,
      max_flows: 800,
      used_value_usd: 1.88,
      max_value_usd: 26.27,
      resets_at: null,
    },
    quota_7_day: {
      usage_percentage: 0.24,
      used_flows: 240,
      remaining_flows: 760,
      max_flows: 800,
      used_value_usd: null,
      max_value_usd: 26.35,
      resets_at: null,
    },
    quota_monthly: {
      max_flows: 10000,
      max_value_usd: 328.3,
    },
  },
}

const createdCredential: ZenMuxCredential = {
  id: '9',
  name: 'AI 绑定账号',
  api_key_preview: 'sk-m-****1234',
  endpoint: 'https://zenmux.ai/api/v1/management/payg/balance',
  proxy_url: 'http://127.0.0.1:7890',
  auth_index: 'ai-1',
  auth_type: 'ai-provider',
  check: null,
  stats: null,
  subscription: null,
  created_at: '2026-08-29T12:00:00Z',
  updated_at: '2026-08-29T12:00:00Z',
}

const setInputValue = (input: HTMLInputElement, value: string) => {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('ZenMuxCredentialsCard', () => {
  it('renders the credential list with balance, Keeper statistics, and the binding type label', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard initialItems={[verifiedCredential]} />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_title')
    expect(html).toContain('usage_stats.credentials_zenmux_subtitle')
    expect(html).toContain('usage_stats.credentials_count')
    expect(html).toContain('usage_stats.credentials_zenmux_add')
    expect(html).toContain('主账号')
    expect(html).toContain('sk-m-****abcd')
    expect(html).toContain('https://zenmux.ai/api/v1/management/payg/balance')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_identity')
    // 静态渲染未触发身份拉取，绑定名回退到原始 auth_index
    expect(html).toContain('auth-xyz')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_auth_file')
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
      <ZenMuxCredentialsCard initialItems={[]} />,
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
      proxy_url: '',
      auth_index: null,
      auth_type: null,
      check: {
        status: 'failed',
        checked_at: '2026-08-29T11:00:00Z',
        total_balance: null,
        top_up_credits: null,
        bonus_credits: null,
        error: 'HTTP 401: invalid management key',
      },
      stats: null,
      subscription: null,
      created_at: '2026-08-02T00:00:00Z',
      updated_at: '2026-08-29T11:00:00Z',
    }

    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard initialItems={[failedCredential]} />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_verify_failed')
    expect(html).toContain('HTTP 401: invalid management key')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_none')
    expect(html).not.toContain('usage_stats.credentials_zenmux_bind_auth_file')
    expect(html).not.toContain('usage_stats.credentials_zenmux_never_verified')
    expect(html).not.toContain('usage_stats.credentials_zenmux_quota_5h')
    expect(html).not.toContain('ULTRA')
    // 未绑定身份时余额与 Keeper 统计都显示占位符 '-'
    expect(html.match(/>-</g)?.length ?? 0).toBeGreaterThanOrEqual(4)
  })

  it('renders every modal form field including the proxy URL input', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialForm
        mode="edit"
        name="主账号"
        apiKey=""
        endpoint="https://zenmux.ai/api/v1/management/payg/balance"
        proxyUrl=""
        authIndex="auth-xyz"
        apiKeyPreview="sk-m-****abcd"
        authFileOptions={[{ value: 'auth-xyz', label: 'Auth Xyz' }]}
        aiProviderOptions={[]}
        onChange={() => undefined}
      />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_name')
    expect(html).toContain('usage_stats.credentials_zenmux_api_key')
    expect(html).toContain('usage_stats.credentials_zenmux_endpoint')
    expect(html).toContain('usage_stats.credentials_zenmux_proxy_url')
    expect(html).toContain('usage_stats.credentials_zenmux_proxy_url_placeholder')
    expect(html).toContain('usage_stats.credentials_zenmux_proxy_url_hint')
    expect(html).toContain('usage_stats.credentials_zenmux_bind_identity')
    expect(html).toContain('sk-m-****abcd')
    expect(html).toContain('usage_stats.credentials_zenmux_api_key_keep')
    expect(html).toContain('usage_stats.credentials_zenmux_api_key_hint')
    expect(html).toContain('Auth Xyz')
  })

  it('uses the API key placeholder hint in create mode', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialForm
        mode="create"
        name=""
        apiKey=""
        endpoint="https://zenmux.ai/api/v1/management/payg/balance"
        proxyUrl=""
        authIndex=""
        authFileOptions={[]}
        aiProviderOptions={[]}
        onChange={() => undefined}
      />,
    )

    expect(html).toContain('usage_stats.credentials_zenmux_api_key_placeholder')
    expect(html).toContain('usage_stats.credentials_zenmux_api_key_hint')
    expect(html).toContain('usage_stats.credentials_zenmux_proxy_url_hint')
    expect(html).not.toContain('usage_stats.credentials_zenmux_api_key_keep')
  })

  it('builds the binding dropdown from self-fetched identities grouped into Auth Files and AI Providers', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await mountCardWithSettledFetches(root, { identities: [authFileIdentity, aiProviderIdentity] })

    const addButton = [...document.querySelectorAll('button')]
      .find((button) => button.textContent === 'usage_stats.credentials_zenmux_add') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const bindTrigger = document.querySelector('button[aria-label="usage_stats.credentials_zenmux_bind_identity"]') as HTMLButtonElement
    await act(async () => { bindTrigger.click() })

    const optionTexts = [...document.querySelectorAll('[role="option"]')].map((element) => element.textContent)
    expect(optionTexts).toContain('Auth Xyz')
    expect(optionTexts).toContain('AI Provider One')
    expect(document.body.textContent).toContain('usage_stats.credentials_zenmux_group_auth_files')
    expect(document.body.textContent).toContain('usage_stats.credentials_zenmux_group_ai_providers')

    const aiOption = [...document.querySelectorAll('[role="option"]')]
      .find((element) => element.textContent === 'AI Provider One') as HTMLButtonElement
    await act(async () => { aiOption.click() })
    // 选中后触发器展示所选身份
    expect(bindTrigger.textContent).toContain('AI Provider One')

    await act(async () => root.unmount())
    document.body.removeChild(container)
  })

  it('submits an AI Provider binding with auth_type and proxy_url on create', async () => {
    const createRequest = deferred<ZenMuxCredential>()
    vi.mocked(createZenMuxCredential).mockReturnValue(createRequest.promise)
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await mountCardWithSettledFetches(root, { identities: [authFileIdentity, aiProviderIdentity] })

    const addButton = [...document.querySelectorAll('button')]
      .find((button) => button.textContent === 'usage_stats.credentials_zenmux_add') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const modalInputs = [...document.querySelectorAll<HTMLInputElement>('.modal-body input')]
    const nameInput = modalInputs.find((input) => input.placeholder === 'usage_stats.credentials_zenmux_name_placeholder')!
    const apiKeyInput = modalInputs.find((input) => input.type === 'password')!
    const proxyInput = modalInputs.find((input) => input.placeholder === 'usage_stats.credentials_zenmux_proxy_url_placeholder')!
    await act(async () => {
      setInputValue(nameInput, 'AI 绑定账号')
      setInputValue(apiKeyInput, 'sk-m-test-1234')
      setInputValue(proxyInput, 'http://127.0.0.1:7890')
    })

    const bindTrigger = document.querySelector('button[aria-label="usage_stats.credentials_zenmux_bind_identity"]') as HTMLButtonElement
    await act(async () => { bindTrigger.click() })
    const aiOption = [...document.querySelectorAll('[role="option"]')]
      .find((element) => element.textContent === 'AI Provider One') as HTMLButtonElement
    await act(async () => { aiOption.click() })

    const saveButton = [...document.querySelectorAll('.modal-footer button')]
      .find((button) => button.textContent === 'common.save') as HTMLButtonElement
    await act(async () => { saveButton.click() })
    expect(vi.mocked(createZenMuxCredential)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(createZenMuxCredential).mock.calls[0][0]).toMatchObject({
      name: 'AI 绑定账号',
      api_key: 'sk-m-test-1234',
      endpoint: 'https://zenmux.ai/api/v1/management/payg/balance',
      proxy_url: 'http://127.0.0.1:7890',
      auth_index: 'ai-1',
      auth_type: 'ai-provider',
    })

    // 在 act 内完成创建请求并等待其 promise，冲刷 handleSave 的后续状态更新
    await act(async () => {
      createRequest.resolve(createdCredential)
      await createRequest.promise
    })
    // 提交后列表行展示 AI Provider 类型标签
    await vi.waitFor(() => {
      expect(document.body.textContent).toContain('usage_stats.credentials_zenmux_bind_ai_provider')
    })

    await act(async () => root.unmount())
    document.body.removeChild(container)
  })

  it('degrades to Not bound only and still submits unbound when identity fetch fails', async () => {
    const createRequest = deferred<ZenMuxCredential>()
    vi.mocked(createZenMuxCredential).mockReturnValue(createRequest.promise)
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await mountCardWithSettledFetches(root, { identitiesError: new Error('network down') })

    const addButton = [...document.querySelectorAll('button')]
      .find((button) => button.textContent === 'usage_stats.credentials_zenmux_add') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const bindTrigger = document.querySelector('button[aria-label="usage_stats.credentials_zenmux_bind_identity"]') as HTMLButtonElement
    await act(async () => { bindTrigger.click() })
    const optionTexts = [...document.querySelectorAll('[role="option"]')].map((element) => element.textContent)
    expect(optionTexts).toEqual(['usage_stats.credentials_zenmux_bind_none'])
    await act(async () => { bindTrigger.click() })

    const modalInputs = [...document.querySelectorAll<HTMLInputElement>('.modal-body input')]
    const nameInput = modalInputs.find((input) => input.placeholder === 'usage_stats.credentials_zenmux_name_placeholder')!
    const apiKeyInput = modalInputs.find((input) => input.type === 'password')!
    await act(async () => {
      setInputValue(nameInput, '未绑定账号')
      setInputValue(apiKeyInput, 'sk-m-test-5678')
    })

    const saveButton = [...document.querySelectorAll('.modal-footer button')]
      .find((button) => button.textContent === 'common.save') as HTMLButtonElement
    await act(async () => { saveButton.click() })
    expect(vi.mocked(createZenMuxCredential).mock.calls[0][0]).toMatchObject({
      name: '未绑定账号',
      auth_index: null,
      auth_type: null,
    })

    await act(async () => root.unmount())
    document.body.removeChild(container)
  })

  it('restores the bound identity on edit from the matching dropdown group', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await mountCardWithSettledFetches(root, {
      identities: [authFileIdentity, aiProviderIdentity],
      items: [createdCredential],
    })

    const editButton = [...document.querySelectorAll('button')]
      .find((button) => button.textContent === 'common.edit') as HTMLButtonElement
    expect(editButton).toBeDefined()
    await act(async () => { editButton.click() })

    const bindTrigger = document.querySelector('button[aria-label="usage_stats.credentials_zenmux_bind_identity"]') as HTMLButtonElement
    expect(bindTrigger.textContent).toContain('AI Provider One')

    await act(async () => root.unmount())
    document.body.removeChild(container)
  })

  it('renders subscription badges and quota windows when subscription is present', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard initialItems={[subscribedCredential]} />,
    )

    expect(html).toContain('ULTRA')
    expect(html).toContain('healthy')
    // healthy 状态使用成功色系徽章
    expect(html).toContain('credentialBadgeSuccess')
    expect(html).toContain('usage_stats.credentials_zenmux_quota_5h')
    // 5h 已用 7.2%，配额条展示剩余 92.8%（四舍五入为 93%）与剩余/上限 flows 和金额
    expect(html).toContain('93%')
    expect(html).toContain('742.8/800')
    expect(html).toContain('$24.39/$26.27')
    expect(html).toContain('usage_stats.credentials_zenmux_quota_7d')
    // 7d 已用 24%，剩余 76%；缺少已用金额时只展示上限金额
    expect(html).toContain('76%')
    expect(html).toContain('760/800')
    expect(html).toContain('$26.35')
    expect(html).not.toContain('/$26.35')
    expect(html).toContain('usage_stats.credentials_zenmux_quota_monthly')
    expect(html).toContain('usage_stats.credentials_zenmux_quota_max')
    expect(html).toContain('10.00K')
    expect(html).toContain('$328.30')
    expect(html).toContain('usage_stats.credentials_zenmux_flows')
    // 配额条按剩余比例着色：93%/76% 均为安全色系
    expect(html).toContain('credentialQuotaFillOk')
  })

  it('does not render subscription info when subscription is null', () => {
    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard initialItems={[verifiedCredential]} />,
    )

    expect(html).not.toContain('usage_stats.credentials_zenmux_quota_5h')
    expect(html).not.toContain('usage_stats.credentials_zenmux_quota_7d')
    expect(html).not.toContain('usage_stats.credentials_zenmux_quota_monthly')
    expect(html).not.toContain('usage_stats.credentials_zenmux_flows')
  })

  it('uses a warning tone for a non-healthy account status', () => {
    const warningCredential: ZenMuxCredential = {
      ...verifiedCredential,
      id: '4',
      name: '异常账号',
      subscription: {
        plan_tier: 'pro',
        plan_expires_at: null,
        account_status: 'suspended',
        quota_5_hour: {
          usage_percentage: 0.9,
          used_flows: 720,
          remaining_flows: 80,
          max_flows: 800,
          resets_at: null,
        },
        quota_7_day: {
          usage_percentage: 0.5,
          used_flows: 400,
          remaining_flows: 400,
          max_flows: 800,
          resets_at: null,
        },
        quota_monthly: {
          max_flows: 10000,
          max_value_usd: 328.3,
        },
      },
    }

    const html = renderToStaticMarkup(
      <ZenMuxCredentialsCard initialItems={[warningCredential]} />,
    )

    expect(html).toContain('PRO')
    expect(html).toContain('suspended')
    expect(html).toContain('credentialBadgeWarning')
    expect(html).not.toContain('credentialBadgeSuccess')
    // 5h 已用 90%，剩余 10% 低于 20% 阈值，配额条使用危险色系
    expect(html).toContain('10%')
    expect(html).toContain('credentialQuotaFillDanger')
  })
})
