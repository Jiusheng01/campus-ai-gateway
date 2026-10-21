import {afterEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {ConsoleLogsPage} from './ConsoleLogsPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import {when} from '../lib/format'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

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
    allowed_models: ['campus-chat'],
    allowed_ips: [],
    usage_requests: 12,
    usage_tokens: 345,
  },
]

const rows = [
  {
    request_id: 'req-aaaaaaaaaaaaaaaaaaaa-1',
    api_key_id: 'k-1',
    requested_model: 'campus-chat',
    actual_model: 'campus-chat',
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
    latency_ms: 120,
    status_code: 200,
    error_code: '',
    failover: false,
    attempt_count: 1,
    created_at: '2026-10-20T08:00:00Z',
  },
  {
    request_id: 'req-bbbbbbbbbbbbbbbbbbbb-2',
    api_key_id: 'k-1',
    requested_model: 'campus-embed',
    actual_model: 'campus-embed-v2',
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    latency_ms: 900,
    status_code: 502,
    error_code: 'upstream_5xx',
    failover: true,
    attempt_count: 2,
    created_at: '2026-10-20T09:00:00Z',
  },
]

const detail = {
  ...rows[1],
  attempt_chain: [
    {
      attempt_no: 1,
      provider: 'primary',
      upstream_model: 'upstream-a',
      outcome: 'failed',
      status_code: 502,
      latency_ms: 800,
      error_kind: 'upstream_5xx',
    },
    {
      attempt_no: 2,
      provider: 'backup',
      upstream_model: 'upstream-b',
      outcome: 'served',
      status_code: 200,
      latency_ms: 100,
      error_kind: '',
    },
  ],
}

type Call = {url: string; method: string}

function stubConsole(options: {rows?: unknown[]; listStatus?: number; detailStatus?: number; detail?: unknown} = {}) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method || 'GET'
      calls.push({url, method})
      if (url === '/api/console/keys') return jsonResponse({data: keys})
      if (url.startsWith('/api/console/request-logs/')) {
        if (options.detailStatus && options.detailStatus >= 400) {
          return jsonResponse({error: {code: 'not_found', message: 'Request record not found'}}, options.detailStatus)
        }
        return jsonResponse({data: options.detail ?? detail})
      }
      if (url.startsWith('/api/console/request-logs')) {
        if (options.listStatus && options.listStatus >= 400) {
          return jsonResponse({error: {code: 'database_error', message: 'list failed'}}, options.listStatus)
        }
        return jsonResponse({data: options.rows ?? rows, limit: 50, offset: 0})
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
        <ConsoleLogsPage />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

describe('personal console request records', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the decoded rows with the fields the records actually carry', async () => {
    stubConsole()
    renderPage()

    expect(await screen.findByText('campus-chat')).toBeInTheDocument()
    expect(screen.getByText('campus-embed')).toBeInTheDocument()
    // The actual alias is shown when it differs from the requested one.
    expect(screen.getByText(/实际 campus-embed-v2/)).toBeInTheDocument()

    expect(screen.getByText('30')).toBeInTheDocument()
    expect(screen.getByText('120 ms')).toBeInTheDocument()
    expect(screen.getByText('900 ms')).toBeInTheDocument()
    expect(screen.getByText('200')).toBeInTheDocument()
    expect(screen.getByText('502')).toBeInTheDocument()
    expect(screen.getAllByText(when(rows[0].created_at)).length).toBeGreaterThan(0)
  })

  it('states explicitly that there are no records yet', async () => {
    stubConsole({rows: []})
    renderPage()

    expect(await screen.findByText('还没有调用记录')).toBeInTheDocument()
    expect(screen.getByText(/使用自己的 API Key 发起调用后/)).toBeInTheDocument()
  })

  it('distinguishes an empty filter result from an account with no traffic', async () => {
    stubConsole({rows: []})
    renderPage()
    await screen.findByText('还没有调用记录')

    fireEvent.change(screen.getByPlaceholderText('全部模型'), {target: {value: 'campus-chat'}})
    fireEvent.click(screen.getByRole('button', {name: '查询'}))

    expect(await screen.findByText('没有匹配的调用记录')).toBeInTheDocument()
  })

  it('sends only narrowing filters: model, status and key, never an owner or tenant', async () => {
    const calls = stubConsole()
    renderPage()
    await screen.findByText('campus-chat')

    await screen.findByRole('option', {name: /教学系统/})
    fireEvent.change(screen.getByPlaceholderText('全部模型'), {target: {value: 'campus-embed'}})
    fireEvent.change(screen.getByPlaceholderText('任意状态'), {target: {value: '502'}})
    fireEvent.change(screen.getByLabelText('API Key'), {target: {value: 'k-1'}})
    fireEvent.click(screen.getByRole('button', {name: '查询'}))

    await waitFor(() => {
      const list = calls.filter(c => c.url.startsWith('/api/console/request-logs?') || c.url === '/api/console/request-logs')
      const last = list[list.length - 1]
      expect(last).toBeTruthy()
      const url = new URL(last.url, 'http://gateway.local')
      expect(url.searchParams.get('model')).toBe('campus-embed')
      expect(url.searchParams.get('status')).toBe('502')
      expect(url.searchParams.get('api_key_id')).toBe('k-1')
      expect(url.searchParams.get('limit')).toBe('50')
      expect(url.searchParams.has('owner_user_id')).toBe(false)
      expect(url.searchParams.has('tenant_id')).toBe(false)
    })
  })

  it('opens the detail drawer with the attempt chain', async () => {
    const calls = stubConsole()
    renderPage()

    fireEvent.click(await screen.findByText('campus-embed'))

    const drawer = await screen.findByRole('dialog')
    await waitFor(() => expect(within(drawer).getByText('primary')).toBeInTheDocument())
    expect(within(drawer).getByText('backup')).toBeInTheDocument()
    // The error code appears both in the outcome block and in the attempt row.
    expect(within(drawer).getAllByText('upstream_5xx').length).toBeGreaterThan(0)
    expect(within(drawer).getByText('成功')).toBeInTheDocument()
    expect(within(drawer).getByText('失败')).toBeInTheDocument()
    // The record's own detail endpoint is asked for, once, by id.
    expect(calls.filter(c => c.url === '/api/console/request-logs/req-bbbbbbbbbbbbbbbbbbbb-2')).toHaveLength(1)
  })

  it('reports a record the account may not see as an error rather than an empty drawer', async () => {
    stubConsole({detailStatus: 404})
    renderPage()

    fireEvent.click(await screen.findByText('campus-embed'))
    const drawer = await screen.findByRole('dialog')

    await waitFor(() => expect(within(drawer).getByText('加载失败')).toBeInTheDocument())
    expect(within(drawer).getByText('Request record not found')).toBeInTheDocument()
  })

  it('reports a failed list instead of showing an empty list as a measurement', async () => {
    stubConsole({listStatus: 500})
    renderPage()

    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('还没有调用记录')).not.toBeInTheDocument()
  })
})
