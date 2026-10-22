import {describe, expect, it, vi, afterEach} from 'vitest'
import {act, render, screen, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {DashboardPage} from './DashboardPage'
import {ToastProvider} from '../components/ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const zeroStats = {
  today_requests: 0, today_tokens: 0, today_errors: 0, avg_latency_ms: 0, p95_latency_ms: 0, failover_count: 0,
}

const status = {
  gateway: true, postgres: true, redis: true, data_plane: 'direct',
  stats: zeroStats,
  provider_stats: [],
  config_counts: {models: 0, enabled_models: 0, providers: 0, enabled_providers: 0},
  route_cache: {built_at: null, stale: false, generation: 0},
}

type Bucket = {bucket: string; requests: number; tokens: number; errors: number; avg_latency_ms: number; p95_latency_ms: number}

const pad = (n: number) => String(n).padStart(2, '0')
const dayKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
const hourLabel = (d: Date) => `${dayKey(d)} ${pad(d.getHours())}:00`

type WindowTotals = {requests: number; tokens: number; errors: number; p95: number}

/**
 * An hourly series with two buckets yesterday and two today.
 *
 * The component compares complete hours only, so it drops today's newest
 * bucket and matches the remainder against the same number of hours at the
 * start of yesterday. One bucket per day is therefore compared with one
 * bucket, which keeps the expected deltas independent of the clock.
 */
function trendSeries(prior: WindowTotals, current: WindowTotals) {
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 24 * 3600 * 1000)
  const row = (d: Date, v: WindowTotals): Bucket => ({
    bucket: hourLabel(d),
    requests: v.requests,
    tokens: v.tokens,
    errors: v.errors,
    avg_latency_ms: v.p95,
    p95_latency_ms: v.p95,
  })
  const data: Bucket[] = []
  for (let i = 0; i < 2; i++) data.push(row(new Date(yesterday.getTime() + i * 3600 * 1000), prior))
  for (let i = 0; i < 2; i++) data.push(row(new Date(today.getTime() + i * 3600 * 1000), current))
  return {data, bucket: 'hour', from: yesterday.toISOString(), to: now.toISOString()}
}

const zeroTrend = trendSeries(
  {requests: 0, tokens: 0, errors: 0, p95: 0},
  {requests: 0, tokens: 0, errors: 0, p95: 0},
)

function route(alias: string) {
  return {
    id: `route-${alias}`, model_id: `model-${alias}`, model_alias: alias, model_display_name: alias,
    provider_id: 'provider-1', provider_name: '渠道一', upstream_model: alias, priority: 1, weight: 100,
    timeout_ms: 30000, status: 1, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    config_version: 1, provider_status: 1, credential_configured: true,
  }
}

function tenant(id: string) {
  return {
    id, code: id, name: id, status: 1, daily_token_limit: 0, monthly_token_limit: 0,
    monthly_request_limit: 0, monthly_budget_micros: 0, rpm_limit: 0, tpm_limit: 0, max_concurrency: 0,
  }
}

type StubOptions = {
  dashboard?: unknown
  trend?: unknown
  routes?: ReturnType<typeof route>[]
  models?: unknown[]
  tenants?: unknown[]
}

function modelForRoute(r: ReturnType<typeof route>) {
  return {id: r.model_id, alias: r.model_alias, route_count: 1}
}

function stubFetch({dashboard = status, trend = zeroTrend, routes = [], models, tenants = []}: StubOptions = {}) {
  // The models response is the count source: default it to one route per model
  // so the sum equals the route list unless a test supplies its own rows.
  const modelRows = models ?? routes.map(modelForRoute)
  const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    const url = String(input)
    // The trend path shares the /admin/status prefix, so it has to be matched
    // first or the status payload would be decoded as a series.
    if (url.startsWith('/admin/status/trend')) return jsonResponse(trend)
    if (url.startsWith('/admin/status')) return jsonResponse(dashboard)
    if (url.startsWith('/admin/routes')) {
      // Mirror the storage layer: one page is 200 rows by default and never
      // more than 500, however large a limit the caller asks for. A stub that
      // echoes the request would hide a count taken from one truncated page.
      const params = new URL(url, 'http://console.local').searchParams
      const limit = Math.min(Number(params.get('limit') ?? 200), 500)
      const offset = Number(params.get('offset') ?? 0)
      return jsonResponse({data: routes.slice(offset, offset + limit), limit, offset})
    }
    if (url.startsWith('/admin/models')) return jsonResponse({data: modelRows})
    if (url.startsWith('/admin/model-test-options')) return jsonResponse({data: []})
    if (url.startsWith('/admin/tenants')) return jsonResponse({data: tenants})
    // A fresh database has no request rows; the API sends data: null.
    if (url.startsWith('/admin/request-logs')) return jsonResponse({data: null})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

/** Let every already-resolved promise and its React update run. */
async function settle() {
  await act(async () => {
    await new Promise(resolve => setTimeout(resolve, 0))
  })
}

function renderPage(roles: string[] = ['super_admin']) {
  return render(
    <ToastProvider>
      <DashboardPage roles={roles} />
    </ToastProvider>,
  )
}

describe('DashboardPage on a fresh deployment', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the empty state instead of crashing when the log is empty', async () => {
    stubFetch()
    renderPage()
    expect(await screen.findByText('配置概况')).toBeInTheDocument()
    expect(await screen.findByText('暂无调用记录')).toBeInTheDocument()
  })

  it('renders no delta, arrow or sparkline when neither window has traffic', async () => {
    stubFetch()
    const {container} = renderPage()
    await screen.findByText('配置概况')
    await settle()

    // A zero bucket is real data, but a period-over-period percentage from a
    // zero baseline is not a fact the response supports.
    expect(screen.queryAllByText(/较昨日/)).toHaveLength(0)
    expect(screen.queryAllByText(/[↑↓]/)).toHaveLength(0)
    expect(container.querySelector('.statCardTrend')).toBeNull()
    expect(container.querySelector('.miniSparkline')).toBeNull()
    // The removed literals must not return through a fallback.
    for (const literal of ['12 ms', '8 ms', '3 ms', '6 ms']) {
      expect(screen.queryByText(literal)).toBeNull()
    }
  })
  it('identifies undefined rates and latency when no requests exist', async () => {
    stubFetch()
    renderPage()
    const rate = (await screen.findByText('错误率')).closest('.statCard') as HTMLElement
    const latency = screen.getByText('P95 延迟').closest('.statCard') as HTMLElement
    expect(within(rate).getByText('—')).toBeInTheDocument()
    expect(within(rate).getByText('暂无请求')).toBeInTheDocument()
    expect(within(latency).getByText('—')).toBeInTheDocument()
    expect(within(latency).getByText('暂无请求')).toBeInTheDocument()
    expect(screen.getByText(/日统计按服务端日界/)).toBeInTheDocument()
  })
  it('shows a recent-request error and reloads it instead of presenting an empty result', async () => {
    const fetchMock = stubFetch()
    const base = fetchMock.getMockImplementation()!
    let failed = true
    fetchMock.mockImplementation(async input => String(input).startsWith('/admin/request-logs') && failed
      ? jsonResponse({error: {message: '记录服务暂不可用'}}, 500)
      : base(input))
    renderPage()
    const panel = (await screen.findByText('最近请求')).closest('.panel') as HTMLElement
    expect(await within(panel).findByText('记录服务暂不可用')).toBeInTheDocument()
    expect(within(panel).queryByText('暂无调用记录')).not.toBeInTheDocument()
    failed = false
    await userEvent.click(within(panel).getByRole('button', {name: '重新加载'}))
    expect(await within(panel).findByText('暂无调用记录')).toBeInTheDocument()
  })
})

describe('DashboardPage trends', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('computes each caption from the two complete-hour windows', async () => {
    stubFetch({
      dashboard: {
        ...status,
        stats: {...zeroStats, today_requests: 150, today_tokens: 1500, today_errors: 6, avg_latency_ms: 80, p95_latency_ms: 120},
      },
      trend: trendSeries(
        {requests: 100, tokens: 1000, errors: 2, p95: 100},
        {requests: 150, tokens: 1500, errors: 6, p95: 120},
      ),
    })
    const {container} = renderPage()

    // 150 vs 100 requests and 1500 vs 1000 tokens are both +50.0%.
    expect(await screen.findAllByText(/较昨日同期 \+50\.0%/)).toHaveLength(2)
    // One trend response feeds all four sparklines.
    expect(container.querySelectorAll('.miniSparkline')).toHaveLength(4)
    // Error rate moved from 2% to 4%: +2.00 percentage points.
    expect(screen.getByText(/较昨日同期 \+2\.00 个百分点/)).toBeInTheDocument()
    // Latency compares the worst complete hour (120 ms vs 100 ms): +20.0%.
    expect(screen.getByText(/较昨日同期峰值 \+20\.0%/)).toBeInTheDocument()
  })

  it('omits a caption for a metric whose prior window has no rows', async () => {
    stubFetch({
      trend: trendSeries(
        {requests: 0, tokens: 0, errors: 0, p95: 0},
        {requests: 30, tokens: 300, errors: 3, p95: 50},
      ),
    })
    renderPage()
    await screen.findByText('配置概况')
    await settle()
    expect(screen.queryAllByText(/较昨日/)).toHaveLength(0)
  })
})

describe('DashboardPage route facts', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('prints the whole route total, not the five-row snapshot size', async () => {
    const routes = Array.from({length: 12}, (_, i) => route(`m${i + 1}`))
    stubFetch({routes})
    renderPage()

    const tile = (await screen.findByText('模型线路')).closest('.configTile')
    expect(tile).not.toBeNull()
    // The count arrives with the model list, after the tile label renders.
    expect(await within(tile as HTMLElement).findByText('12')).toBeInTheDocument()
    // The table beside it is still a five-row sample.
    expect(screen.queryByText('m6')).toBeNull()
  })

  it('counts every route when the route endpoint truncates its page at 200', async () => {
    // 250 routes are more than one page: /admin/routes returns 200 by default.
    // Counting the page would print 200 for a deployment that has 250.
    const routes = Array.from({length: 250}, (_, i) => route(`m${i + 1}`))
    stubFetch({
      routes,
      models: [
        {id: 'model-a', alias: 'a', route_count: 100},
        {id: 'model-b', alias: 'b', route_count: 100},
        {id: 'model-c', alias: 'c', route_count: 50},
      ],
    })
    renderPage()

    const tile = (await screen.findByText('模型线路')).closest('.configTile')
    expect(tile).not.toBeNull()
    expect(await within(tile as HTMLElement).findByText('250')).toBeInTheDocument()
    expect(within(tile as HTMLElement).queryByText('200')).toBeNull()
  })

  it('shows the staleness warning with its reason only when the cache is stale', async () => {
    stubFetch({
      dashboard: {
        ...status,
        route_cache: {built_at: '2026-10-20T08:00:00Z', stale: true, last_error_kind: 'rebuild_failed', generation: 7},
      },
      routes: [route('m1')],
    })
    renderPage()
    expect(await screen.findByText(/重建失败/)).toBeInTheDocument()
    expect(screen.getByText(/rebuild_failed/)).toBeInTheDocument()
  })

  it('renders no staleness warning when the cache is fresh', async () => {
    stubFetch({routes: [route('m1')]})
    renderPage()
    await screen.findByText('模型线路')
    await settle()
    expect(screen.queryByText(/重建失败/)).toBeNull()
  })

  it('feeds the tenant tile from the tenant list for a role that may read it', async () => {
    stubFetch({tenants: [tenant('t1'), tenant('t2'), tenant('t3')]})
    renderPage(['super_admin'])
    const tile = (await screen.findByText('租户')).closest('.configTile')
    expect(tile).not.toBeNull()
    // The count arrives with the tenant list, after the tile label renders.
    expect(await within(tile as HTMLElement).findByText('3')).toBeInTheDocument()
  })

  it('does not ask for tenants with a role the endpoint refuses', async () => {
    const fetchMock = stubFetch({tenants: [tenant('t1')]})
    renderPage(['operator'])
    await screen.findByText('租户')
    await settle()
    expect(fetchMock.mock.calls.some(([url]) => String(url).startsWith('/admin/tenants'))).toBe(false)
  })
  it('does not report a partial route total when a model count is absent', async () => {
    stubFetch({models: [{id: 'a', alias: 'a', route_count: 3}, {id: 'b', alias: 'b'}]})
    renderPage()
    const tile = (await screen.findByText('模型线路')).closest('.configTile') as HTMLElement
    await settle()
    expect(within(tile).getByText('—')).toBeInTheDocument()
    expect(within(tile).queryByText('3')).not.toBeInTheDocument()
  })
})

describe('DashboardPage quick actions', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('opens the real channel creation form for a configuration administrator', async () => {
    stubFetch()
    renderPage(['gateway_admin'])
    await userEvent.click(await screen.findByRole('button', {name: '新增渠道'}))
    expect(screen.getByRole('dialog', {name: '新增渠道'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '创建渠道'})).toBeInTheDocument()
  })
  it('creates a channel with a supported protocol and refreshes the overview', async () => {
    const fetchMock = stubFetch()
    const base = fetchMock.getMockImplementation()!
    fetchMock.mockImplementation(async (input, init) => String(input) === '/admin/providers' && init?.method === 'POST'
      ? jsonResponse({id: 'new-provider'}) : base(input, init))
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('button', {name: '新增渠道'}))
    const dialog = screen.getByRole('dialog', {name: '新增渠道'})
    await user.type(within(dialog).getByRole('textbox', {name: /渠道名称/}), 'campus-provider')
    await user.type(within(dialog).getByRole('textbox', {name: 'Base URL'}), 'https://models.example.edu/v1')
    await user.click(within(dialog).getByRole('checkbox', {name: '改为引用环境变量'}))
    await user.type(within(dialog).getByRole('textbox', {name: /凭据环境变量/}), 'CAMPUS_PROVIDER_TEST')
    await user.click(within(dialog).getByRole('button', {name: '创建渠道'}))
    expect(await screen.findByText(/渠道已创建/)).toBeInTheDocument()
    const write = fetchMock.mock.calls.find(([input, init]) => String(input) === '/admin/providers' && init?.method === 'POST')
    expect(JSON.parse(String(write?.[1]?.body))).toMatchObject({name: 'campus-provider', protocol: 'openai-chat', credential_env: 'CAMPUS_PROVIDER_TEST'})
    expect(screen.queryByRole('dialog', {name: '新增渠道'})).not.toBeInTheDocument()
  })
  it('opens publishing from the overview without leaving the current task', async () => {
    const fetchMock = stubFetch()
    const base = fetchMock.getMockImplementation()!
    fetchMock.mockImplementation(async (input, init) => String(input).startsWith('/admin/providers') ? jsonResponse({data: []}) : base(input, init))
    renderPage()
    await userEvent.click(screen.getByRole('button', {name: '发布模型'}))
    expect(await screen.findByRole('dialog', {name: '发布模型'})).toBeInTheDocument()
  })
  it('gives an operator diagnostic access without management reads or actions', async () => {
    const fetchMock = stubFetch()
    renderPage(['operator'])
    await screen.findByText('配置概况')
    expect(screen.queryByRole('button', {name: '新增渠道'})).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: '发布模型'})).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', {name: '模型连通测试'}))
    expect(await screen.findByRole('dialog', {name: '模型连通测试'})).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([url]) => /^\/admin\/(routes|models|tenants|request-logs)/.test(String(url)))).toBe(false)
  })
})
