import {afterEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {ConsoleOverviewPage} from './ConsoleOverviewPage'

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
  allowed_models: ['*'],
  workspace_usable: true,
  key_count: 2,
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

const usage = {
  from: '2026-10-21T00:00:00Z',
  to: '2026-10-21T12:00:00Z',
  bucket: 'hour' as const,
  totals: {
    requests: 3,
    successes: 2,
    failures: 1,
    input_tokens: 37,
    output_tokens: 8,
    total_tokens: 45,
    error_rate: 33.3333,
  },
  series: [
    {bucket: '2026-10-21 08:00', requests: 2, tokens: 30, errors: 0, avg_latency_ms: 10, p95_latency_ms: 20},
    {bucket: '2026-10-21 09:00', requests: 1, tokens: 15, errors: 1, avg_latency_ms: 10, p95_latency_ms: 20},
  ],
  by_model: [],
  tenant_quota: null,
  cost_available: false,
}

const logs = {
  data: [
    {
      request_id: 'req-1',
      api_key_id: 'k-1',
      requested_model: 'campus-chat',
      actual_model: 'campus-chat',
      input_tokens: 10,
      output_tokens: 5,
      total_tokens: 15,
      latency_ms: 120,
      status_code: 200,
      error_code: '',
      failover: false,
      attempt_count: 1,
      created_at: '2026-10-21T08:00:00Z',
    },
    {
      request_id: 'req-2',
      api_key_id: 'k-1',
      requested_model: 'campus-embed',
      actual_model: 'campus-embed-v2',
      input_tokens: 7,
      output_tokens: 3,
      total_tokens: 10,
      latency_ms: 80,
      status_code: 500,
      error_code: 'upstream_error',
      failover: true,
      attempt_count: 2,
      created_at: '2026-10-21T09:00:00Z',
    },
  ],
  limit: 5,
  offset: 0,
}

const models = {
  models: [
    {alias: 'campus-chat', display_name: '校园问答', description: '', available: true},
    {alias: 'campus-embed', display_name: '校园向量', description: '', available: true},
  ],
}

const detail = {
  ...logs.data[1],
  attempt_chain: [
    {
      attempt_no: 1,
      provider: 'primary',
      upstream_model: 'upstream-a',
      outcome: 'failed',
      status_code: 500,
      latency_ms: 60,
      error_kind: 'upstream_error',
    },
    {
      attempt_no: 2,
      provider: 'backup',
      upstream_model: 'upstream-b',
      outcome: 'served',
      status_code: 200,
      latency_ms: 20,
      error_kind: '',
    },
  ],
}

type Options = {
  me?: Record<string, unknown>
  meStatus?: number
  keys?: unknown[]
  usage?: unknown
  logs?: unknown
  detail?: unknown
  models?: unknown
}

/**
 * Stubs every console endpoint the overview can reach. The call log is
 * returned so a test can assert the unassigned page did not ask for numbers it
 * has no workspace to interpret.
 */
function stubConsole(options: Options = {}) {
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/console/me') {
        return jsonResponse(
          options.meStatus && options.meStatus >= 400
            ? {error: {code: 'admin_internal_error', message: 'database unavailable'}}
            : {...me, ...(options.me || {})},
          options.meStatus || 200,
        )
      }
      if (url === '/api/console/keys') return jsonResponse({data: options.keys ?? keys})
      if (url.startsWith('/api/console/usage')) return jsonResponse(options.usage ?? usage)
      if (url.startsWith('/api/console/request-logs/')) {
        return jsonResponse({data: options.detail ?? detail})
      }
      if (url.startsWith('/api/console/request-logs')) return jsonResponse(options.logs ?? logs)
      if (url === '/api/console/models') return jsonResponse(options.models ?? models)
      return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
    }),
  )
  return calls
}

/** The value inside the stat card whose label is `label`. */
function statCard(label: string): HTMLElement {
  const labelEl = screen.getByText(label, {selector: '.statCardLabel'})
  return labelEl.closest('.statCard') as HTMLElement
}

describe('personal console overview', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the account and an explicit unassigned-Tenant state, never zero KPIs', async () => {
    const calls = stubConsole({
      me: {tenant_id: '', tenant_name: '', allowed_models: [], workspace_usable: false},
      keys: [],
      usage: {...usage, totals: {...usage.totals, requests: 0, successes: 0, failures: 0, total_tokens: 0, error_rate: 0}, series: []},
      logs: {data: [], limit: 5, offset: 0},
    })
    const {container} = render(<ConsoleOverviewPage />)

    expect(await screen.findByText('member-one')).toBeInTheDocument()
    expect(screen.getByText('校园用户')).toBeInTheDocument()
    expect(screen.getByText('普通用户')).toBeInTheDocument()
    expect(screen.getByText('正常')).toBeInTheDocument()
    expect(screen.getByText('尚未分配租户')).toBeInTheDocument()
    expect(screen.getByText(/请联系管理员在「用户」中为你的账号分配租户/)).toBeInTheDocument()
    expect(screen.queryByText('可用')).not.toBeInTheDocument()

    // The unassigned workspace mounts no measurement block at all: the key,
    // usage, log and model endpoints were never asked, and no zero card exists.
    expect(container.querySelectorAll('.statCard')).toHaveLength(0)
    expect(screen.queryByText('今日请求')).not.toBeInTheDocument()
    expect(screen.queryByText('我的 API Key')).not.toBeInTheDocument()
    expect(calls).toEqual(['/api/console/me'])
  })

  it('shows the assigned Tenant and a usable workspace', async () => {
    stubConsole()
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('可用')).toBeInTheDocument()
    // The Tenant name appears in both the account facts and the membership row.
    expect(screen.getAllByText('校园信息化').length).toBeGreaterThan(0)
    expect(screen.queryByText('尚未分配租户')).not.toBeInTheDocument()
  })

  it('prompts for a first key and links to the key page instead of showing usage zeros', async () => {
    stubConsole({keys: []})
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('还没有 API Key')).toBeInTheDocument()
    expect(screen.getByText(/创建你的第一个 API Key/)).toBeInTheDocument()
    expect(screen.getByRole('link', {name: '创建 API Key'})).toHaveAttribute('href', '/console/keys')

    // With no credential there is nothing to measure yet, so no traffic cards.
    expect(screen.queryByText('今日请求')).not.toBeInTheDocument()
    expect(screen.queryByText('今日 Token')).not.toBeInTheDocument()

    // The catalog entry is still useful before the first key exists.
    expect(screen.getByText('你的租户当前允许调用 2 个模型。')).toBeInTheDocument()
  })

  it('renders the decoded figures, the trend and the recent records', async () => {
    const calls = stubConsole()
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('今日概览')).toBeInTheDocument()

    expect(within(statCard('我的 API Key')).getByText('2')).toBeInTheDocument()
    expect(within(statCard('已启用 Key')).getByText('1')).toBeInTheDocument()
    expect(within(statCard('今日请求')).getByText('3')).toBeInTheDocument()
    expect(within(statCard('今日 Token')).getByText('45')).toBeInTheDocument()
    expect(within(statCard('今日错误率')).getByText('33.33%')).toBeInTheDocument()

    // The trend is the series the usage endpoint returned, not a synthetic one.
    expect(screen.getByRole('img', {name: '按时间分组的请求数'})).toBeInTheDocument()

    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getByText('campus-chat')).toBeInTheDocument()
    expect(within(table).getByText('campus-embed')).toBeInTheDocument()
    expect(within(table).getByText('500')).toBeInTheDocument()
    expect(screen.getByRole('link', {name: '查看全部'})).toHaveAttribute('href', '/console/logs')

    // The models entry uses models.length.
    expect(screen.getByText('你的租户当前允许调用 2 个模型。')).toBeInTheDocument()

    // The recent list asked for a short page, not the whole history.
    expect(calls.some(url => url.startsWith('/api/console/request-logs') && url.includes('limit=5'))).toBe(true)
  })

  it('opens a recent record in the same detail drawer as the log page', async () => {
    stubConsole()
    render(<ConsoleOverviewPage />)
    await screen.findByText('今日概览')

    fireEvent.click(screen.getByText('campus-embed'))

    const drawer = await screen.findByRole('dialog')
    await waitFor(() => expect(within(drawer).getByText('primary')).toBeInTheDocument())
    expect(within(drawer).getByText('backup')).toBeInTheDocument()
    expect(within(drawer).getByText('成功')).toBeInTheDocument()
    expect(within(drawer).getByText('失败')).toBeInTheDocument()
  })

  it('distinguishes keys that exist from traffic that does not', async () => {
    stubConsole({logs: {data: [], limit: 5, offset: 0}})
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('还没有任何调用')).toBeInTheDocument()
    expect(screen.getByText(/使用个人 API Key 发起第一次调用后/)).toBeInTheDocument()
    expect(within(statCard('我的 API Key')).getByText('2')).toBeInTheDocument()
    expect(within(statCard('已启用 Key')).getByText('1')).toBeInTheDocument()

    // Nothing has been called, so no traffic figure is rendered as a zero.
    expect(screen.queryByText('今日请求')).not.toBeInTheDocument()
    expect(screen.queryByText('今日 Token')).not.toBeInTheDocument()
    expect(screen.queryByText('今日错误率')).not.toBeInTheDocument()
  })

  it('names the reason the catalog is empty', async () => {
    stubConsole({models: {models: [], empty_reason: 'tenant_allows_none'}})
    render(<ConsoleOverviewPage />)

    expect(
      await screen.findByText('你的租户当前未开放任何模型。请联系管理员在租户设置中开放所需模型。'),
    ).toBeInTheDocument()
  })

  it('reports a failed read instead of inventing an unassigned state', async () => {
    stubConsole({meStatus: 500})
    render(<ConsoleOverviewPage />)

    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('尚未分配租户')).not.toBeInTheDocument()
  })
})
