// @vitest-environment happy-dom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createZenMuxCredential, fetchUsageIdentities, fetchZenMuxCredentials } from '@/lib/api'
import { ZenMuxCredentialsCard, ZenMuxCredentialForm } from '../ZenMuxCredentialsCard'
import type { UsageIdentity, ZenMuxCredential } from '@/lib/types'

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
  created_at: '2026-08-01T00:00:00Z',
  updated_at: '2026-08-29T10:00:00Z',
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
    expect(html).toContain('usage_stats.credentials_zenmux_proxy_url_hint')
    expect(html).not.toContain('usage_stats.credentials_zenmux_api_key_keep')
  })

  it('builds the binding dropdown from self-fetched identities grouped into Auth Files and AI Providers', async () => {
    vi.mocked(fetchZenMuxCredentials).mockResolvedValue({ items: [] })
    vi.mocked(fetchUsageIdentities).mockResolvedValue({
      identities: [authFileIdentity, aiProviderIdentity],
    })
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<ZenMuxCredentialsCard />)
    })
    await act(async () => {})
    await act(async () => {})

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

    root.unmount()
    document.body.removeChild(container)
  })

  it('submits an AI Provider binding with auth_type and proxy_url on create', async () => {
    vi.mocked(fetchZenMuxCredentials).mockResolvedValue({ items: [] })
    vi.mocked(fetchUsageIdentities).mockResolvedValue({
      identities: [authFileIdentity, aiProviderIdentity],
    })
    const createRequest = deferred<ZenMuxCredential>()
    vi.mocked(createZenMuxCredential).mockReturnValue(createRequest.promise)
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<ZenMuxCredentialsCard />)
    })
    await act(async () => {})
    await act(async () => {})

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

    await act(async () => {})
    await act(async () => {})
    await act(async () => {})
  })

  it('degrades to Not bound only and still submits unbound when identity fetch fails', async () => {
    vi.mocked(fetchZenMuxCredentials).mockResolvedValue({ items: [] })
    vi.mocked(fetchUsageIdentities).mockRejectedValue(new Error('network down'))
    const createRequest = deferred<ZenMuxCredential>()
    vi.mocked(createZenMuxCredential).mockReturnValue(createRequest.promise)
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<ZenMuxCredentialsCard />)
    })
    await act(async () => {})
    await act(async () => {})

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

    root.unmount()
    document.body.removeChild(container)
  })

  it('restores the bound identity on edit from the matching dropdown group', async () => {
    vi.mocked(fetchZenMuxCredentials).mockResolvedValue({ items: [createdCredential] })
    vi.mocked(fetchUsageIdentities).mockResolvedValue({
      identities: [authFileIdentity, aiProviderIdentity],
    })
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<ZenMuxCredentialsCard />)
    })
    await act(async () => {})
    await act(async () => {})
    await act(async () => {})

    const editButton = [...document.querySelectorAll('button')]
      .find((button) => button.textContent === 'common.edit') as HTMLButtonElement
    expect(editButton).toBeDefined()
    await act(async () => { editButton.click() })

    const bindTrigger = document.querySelector('button[aria-label="usage_stats.credentials_zenmux_bind_identity"]') as HTMLButtonElement
    expect(bindTrigger.textContent).toContain('AI Provider One')

    root.unmount()
    document.body.removeChild(container)
  })
})
