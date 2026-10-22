import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {ApiKeysPage} from './ApiKeysPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {ApiKey, ModelConfig, Tenant} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function key(over: Partial<ApiKey> = {}): ApiKey {
  return {
    id: 'k-1',
    tenant_id: 't-1',
    tenant_code: 'CAMPUS',
    tenant_name: '校园信息化',
    name: '校园问答',
    key_prefix: 'sk-campus-abcd',
    status: 1,
    expire_at: '2027-01-02T03:04:05Z',
    last_used_at: '2026-10-20T08:00:00Z',
    allowed_models: ['campus-chat'],
    allowed_ips: [],
    route_group: '',
    usage_requests: 12,
    usage_tokens: 340,
    ...over,
  }
}

const tenant: Tenant = {
  id: 't-1',
  code: 'CAMPUS',
  name: '校园信息化',
  status: 1,
  daily_token_limit: 0,
  monthly_token_limit: 0,
  monthly_request_limit: 0,
  monthly_budget_micros: 0,
  rpm_limit: 0,
  tpm_limit: 0,
  max_concurrency: 0,
  allowed_models: [],
}

const model: ModelConfig = {
  id: 'm-1',
  alias: 'campus-chat',
  display_name: '校园问答',
  description: '',
  status: 1,
  config_version: 1,
}

function renderPage(opts: {keys?: ApiKey[]; tenants?: Tenant[]; models?: ModelConfig[]; onReload?: () => void} = {}) {
  const bundle = {
    keys: opts.keys ?? [key()],
    tenants: opts.tenants ?? [tenant],
    models: opts.models ?? [model],
  }
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <ApiKeysPage bundle={bundle} onReload={opts.onReload ?? vi.fn()} />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ApiKeysPage ownership facts', () => {
  // The tenant is the key's owner. When the response carries no tenant name,
  // inventing "默认租户" asserts an owner the gateway never reported, and an
  // operator debugging a refusal would look at the wrong department.
  it('does not invent a default tenant when the owner name is absent', () => {
    renderPage({keys: [key({tenant_name: '', tenant_code: ''})]})
    expect(screen.queryByText('默认租户')).not.toBeInTheDocument()
    expect(screen.getByText('归属租户未返回')).toBeInTheDocument()
  })

  it('shows the tenant name and code it was given', () => {
    renderPage()
    const row = screen.getByText('校园问答').closest('tr') as HTMLElement
    expect(within(row).getByText('校园信息化')).toBeInTheDocument()
    expect(within(row).getByText('/ CAMPUS')).toBeInTheDocument()
  })
})

describe('ApiKeysPage secret handling', () => {
  it('copies only the prefix, and says so when the clipboard refuses', async () => {
    const writeText = vi.fn(async () => {
      throw new Error('denied')
    })
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText}})
    renderPage()

    await userEvent.click(screen.getByRole('button', {name: '复制 Key 前缀'}))

    // The operator asked for a copy and did not get one; silence would leave
    // them believing the prefix is on the clipboard.
    expect(await screen.findByText(/复制失败/)).toBeInTheDocument()
    expect(writeText).toHaveBeenCalledWith('sk-campus-abcd')
  })

  // The full key is shown once, in the create response, and nowhere else: the
  // list keeps only the prefix.
  it('reveals the full key once after creation and keeps only the prefix afterwards', async () => {
    const created = 'sk-campus-FULL-SECRET-VALUE'
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/admin/api-keys' && init?.method === 'POST') {
        return jsonResponse({id: 'k-2', api_key: created}, 201)
      }
      return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
    }))
    const onReload = vi.fn()
    renderPage({onReload})

    await userEvent.click(screen.getByRole('button', {name: '+ 创建 API Key'}))
    await userEvent.type(screen.getByPlaceholderText('请输入 API Key 名称'), '教学系统')
    await userEvent.click(screen.getByRole('button', {name: '创建 API Key'}))

    expect(await screen.findByText(created)).toBeInTheDocument()
    expect(onReload).toHaveBeenCalled()
    // The list still holds the prefix it was given, never the secret.
    expect(screen.getByText('sk-campus-abcd…')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('checkbox', {name: /我已确认复制/}))
    await userEvent.click(screen.getByRole('button', {name: '完成并关闭'}))
    expect(screen.queryByText(created)).not.toBeInTheDocument()
  })

  it('keeps the create form open and the input intact when the write fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({error: {message: '租户不存在'}}, 400)))
    const onReload = vi.fn()
    renderPage({onReload})

    await userEvent.click(screen.getByRole('button', {name: '+ 创建 API Key'}))
    await userEvent.type(screen.getByPlaceholderText('请输入 API Key 名称'), '教学系统')
    await userEvent.click(screen.getByRole('button', {name: '创建 API Key'}))

    expect(await screen.findByText('租户不存在')).toBeInTheDocument()
    expect(screen.getByDisplayValue('教学系统')).toBeInTheDocument()
    expect(onReload).not.toHaveBeenCalled()
  })

  it('cancels a create without sending anything', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({}))
    vi.stubGlobal('fetch', fetchMock)
    renderPage()

    await userEvent.click(screen.getByRole('button', {name: '+ 创建 API Key'}))
    await userEvent.click(screen.getByRole('button', {name: '取消'}))

    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

describe('ApiKeysPage row actions', () => {
  it('confirms a destructive delete by name and sends nothing when cancelled', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ok: true}))
    vi.stubGlobal('fetch', fetchMock)
    renderPage()

    await userEvent.click(screen.getByRole('button', {name: /删除/}))
    expect(screen.getByRole('button', {name: '确认删除'})).toBeDisabled()
    await userEvent.click(screen.getByRole('button', {name: '取消'}))

    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByText('校园问答')).toBeInTheDocument()
  })

  it('keeps the row when the delete request fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({error: {message: '删除被拒绝'}}, 500)))
    renderPage()

    await userEvent.click(screen.getByRole('button', {name: /删除/}))
    await userEvent.type(screen.getByLabelText(/输入 Key 名称/), '校园问答')
    await userEvent.click(screen.getByRole('button', {name: '确认删除'}))

    expect(await screen.findByText('删除被拒绝')).toBeInTheDocument()
    expect(screen.getByText('校园问答')).toBeInTheDocument()
  })

  it('filters by name, prefix, tenant and model as the operator types', async () => {
    renderPage({
      keys: [
        key(),
        key({id: 'k-2', name: '教务系统', key_prefix: 'sk-teach-9', allowed_models: ['campus-embed'], tenant_name: '教务中心', tenant_code: 'TEACH'}),
      ],
    })

    const search = screen.getByPlaceholderText('搜索 Key 名称、租户或模型...')
    await userEvent.type(search, 'campus-embed')
    await waitFor(() => expect(screen.queryByText('校园问答')).not.toBeInTheDocument())
    expect(screen.getByText('教务系统')).toBeInTheDocument()

    await userEvent.clear(search)
    await userEvent.type(search, 'sk-teach')
    await waitFor(() => expect(screen.getByText('教务系统')).toBeInTheDocument())
    expect(screen.queryByText('校园问答')).not.toBeInTheDocument()
  })
})
