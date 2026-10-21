import {afterEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {ConsoleKeysPage} from './ConsoleKeysPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const me = {
  account_id: 'u-1',
  username: 'member-one',
  display_name: '校园用户',
  roles: ['member'],
  status: 1,
  tenant_id: 't-1',
  tenant_name: '校园信息化',
  allowed_models: ['campus-chat', 'campus-embed'],
  workspace_usable: true,
}

const catalog = [
  {alias: 'campus-chat', display_name: '校园问答', description: '', available: true},
  {alias: 'campus-embed', display_name: '校园向量', description: '', available: true},
]

const keys = [
  {
    id: 'k-1',
    tenant_id: 't-1',
    tenant_code: 'CAMPUS',
    tenant_name: '校园信息化',
    name: '教学系统',
    key_prefix: 'sk-campus-teach',
    status: 1,
    created_at: '2026-10-01T00:00:00Z',
    last_used_at: '2026-10-20T08:00:00Z',
    allowed_models: ['campus-chat'],
    allowed_ips: [],
    usage_requests: 12,
    usage_tokens: 345,
  },
  {
    id: 'k-2',
    tenant_id: 't-1',
    tenant_code: 'CAMPUS',
    tenant_name: '校园信息化',
    name: '实验平台',
    key_prefix: 'sk-campus-lab',
    status: 2,
    created_at: '2026-10-02T00:00:00Z',
    allowed_models: ['*'],
    allowed_ips: [],
    usage_requests: 0,
    usage_tokens: 0,
  },
]

type Call = {url: string; method: string; body?: string}

function stubConsole(options: {
  me?: unknown
  meStatus?: number
  models?: unknown[]
  modelsStatus?: number
  emptyReason?: string
  keys?: unknown[]
  keysStatus?: number
  create?: unknown
} = {}) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method || 'GET'
      calls.push({url, method, body: init?.body as string | undefined})
      if (url === '/api/console/me') {
        return jsonResponse(
          options.meStatus && options.meStatus >= 400
            ? {error: options.me || {code: 'admin_internal_error', message: 'database unavailable'}}
            : {...me, ...(options.me as object)},
          options.meStatus || 200,
        )
      }
      if (url === '/api/console/models') {
        const models = options.models ?? catalog
        return jsonResponse(
          options.modelsStatus && options.modelsStatus >= 400
            ? {error: {code: 'admin_internal_error', message: 'models unavailable'}}
            : {models, ...(options.emptyReason ? {empty_reason: options.emptyReason} : {})},
          options.modelsStatus || 200,
        )
      }
      if (url === '/api/console/keys' && method === 'GET') {
        return jsonResponse(
          options.keysStatus && options.keysStatus >= 400
            ? {error: {code: 'database_error', message: 'list failed'}}
            : {data: options.keys ?? keys},
          options.keysStatus || 200,
        )
      }
      if (url === '/api/console/keys' && method === 'POST') {
        return jsonResponse(options.create ?? {...keys[0], api_key: 'sk-campus-revealed-once'}, 201)
      }
      return jsonResponse({error: {code: 'not_found', message: `unexpected ${method} ${url}`}}, 404)
    }),
  )
  return calls
}

function renderPage() {
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <ConsoleKeysPage />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

describe('personal console API keys', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('lists the keys the endpoint returned and narrows them by search and status', async () => {
    stubConsole()
    renderPage()

    expect(await screen.findByText('教学系统')).toBeInTheDocument()
    expect(screen.getByText('实验平台')).toBeInTheDocument()
    expect(screen.getByText('sk-campus-teach…')).toBeInTheDocument()
    expect(screen.getByText('全部对外模型')).toBeInTheDocument()
    expect(screen.getByText('共 2 条记录')).toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText('搜索 Key 名称、前缀或模型...'), {
      target: {value: '实验'},
    })
    expect(screen.queryByText('教学系统')).not.toBeInTheDocument()
    expect(screen.getByText('实验平台')).toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText('搜索 Key 名称、前缀或模型...'), {target: {value: ''}})
    fireEvent.change(screen.getByLabelText('全部状态'), {target: {value: '2'}})
    expect(screen.queryByText('教学系统')).not.toBeInTheDocument()
    expect(screen.getByText('实验平台')).toBeInTheDocument()
    expect(screen.getByText('共 1 条记录')).toBeInTheDocument()
  })

  it('renders an honest empty state when the account owns no keys', async () => {
    stubConsole({keys: []})
    renderPage()

    expect(await screen.findByText('还没有 API Key')).toBeInTheDocument()
    expect(screen.getByText(/创建你的第一个 API Key/)).toBeInTheDocument()
  })

  it('states that an unassigned account cannot create a key', async () => {
    stubConsole({me: {tenant_id: '', tenant_name: '', allowed_models: [], workspace_usable: false}, keys: []})
    renderPage()

    expect(await screen.findByText('尚未分配租户')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: /创建 API Key/})).toBeDisabled()
  })

  it('states that a Tenant which allows no models cannot issue a key', async () => {
    stubConsole({models: [], emptyReason: 'tenant_allows_none', keys: []})
    renderPage()

    expect(await screen.findByText('租户未开放任何模型')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: /创建 API Key/})).toBeDisabled()
  })

  it('reports a failed list instead of showing an empty list as a measurement', async () => {
    stubConsole({keysStatus: 500})
    renderPage()

    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('还没有 API Key')).not.toBeInTheDocument()
  })

  it('sends only name and scope, then reveals the full key exactly once behind an acknowledgement', async () => {
    const calls = stubConsole()
    renderPage()
    await screen.findByText('教学系统')

    fireEvent.click(screen.getByRole('button', {name: /创建 API Key/}))
    const drawer = await screen.findByRole('dialog')
    fireEvent.change(within(drawer).getByPlaceholderText('请输入 API Key 名称'), {
      target: {value: '测试 Key'},
    })
    fireEvent.click(within(drawer).getByRole('button', {name: '创建 API Key'}))

    await waitFor(() => {
      const post = calls.find(c => c.method === 'POST' && c.url === '/api/console/keys')
      expect(post).toBeTruthy()
      expect(JSON.parse(post!.body || '{}')).toEqual({
        name: '测试 Key',
        allowed_models: ['campus-chat', 'campus-embed'],
      })
    })

    const reveal = await screen.findByRole('dialog')
    expect(within(reveal).getByText('sk-campus-revealed-once')).toBeInTheDocument()

    const done = within(reveal).getByRole('button', {name: '完成并关闭'})
    expect(done).toBeDisabled()
    fireEvent.click(within(reveal).getByLabelText(/我已确认复制并安全妥善保存了此 API Key/))
    expect(done).not.toBeDisabled()

    // The secret exists only in the reveal dialog; the list still has none.
    expect(screen.queryByText(/sk-campus-revealed-once/, {selector: 'code.keyPrefixCode'})).not.toBeInTheDocument()
  })

  it('renders a loading skeleton before the first response arrives', async () => {
    let release = () => {}
    const gate = new Promise<void>(resolve => {
      release = resolve
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        await gate
        return jsonResponse({data: []})
      }),
    )
    renderPage()
    expect(screen.getByLabelText('正在加载')).toBeInTheDocument()
    release?.()
    await waitFor(() => expect(screen.queryByLabelText('正在加载')).not.toBeInTheDocument())
  })
})
