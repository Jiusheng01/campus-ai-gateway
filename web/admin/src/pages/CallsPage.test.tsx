import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {CallsPage} from './CallsPage'
import {ToastProvider} from '../components/ui/Toast'
import {when} from '../lib/format'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const status = {
  gateway: true,
  postgres: true,
  redis: true,
  data_plane: 'direct',
  stats: {today_requests: 4242, today_tokens: 12345, today_errors: 3, avg_latency_ms: 120, p95_latency_ms: 300, failover_count: 1},
  provider_stats: [],
}

const keys = [
  {
    id: 'k1',
    tenant_id: 't1',
    tenant_code: 'CAMPUS',
    tenant_name: '校园信息化',
    name: '校园问答',
    key_prefix: 'sk-abc',
    status: 1,
    expire_at: '2027-01-02T03:04:05Z',
    last_used_at: '2026-10-20T08:00:00Z',
    allowed_models: ['*'],
    allowed_ips: [],
    route_group: '',
    usage_requests: 999999,
    usage_tokens: 888888,
  },
]

const tenants = [
  {id: 't1', code: 'CAMPUS', name: '校园信息化', status: 1, daily_token_limit: 0, monthly_token_limit: 0, monthly_request_limit: 0, monthly_budget_micros: 0, rpm_limit: 0, tpm_limit: 0, max_concurrency: 0},
]

const models = [
  {id: 'm1', alias: 'campus-chat', display_name: '校园问答', description: '', status: 1, config_version: 1},
  {id: 'm2', alias: 'campus-retired', display_name: '已下线', description: '', status: 2, config_version: 1},
]

type StubOptions = {statusFails?: boolean}

/** Installs a fetch stub and returns the list of requested paths, in order. */
function stubFetch({statusFails = false}: StubOptions = {}) {
  const paths: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      paths.push(path)
      if (path === '/admin/status') {
        return statusFails ? jsonResponse({error: {message: 'status unavailable'}}, 503) : jsonResponse(status)
      }
      if (path === '/admin/api-keys') return jsonResponse({data: keys})
      if (path === '/admin/tenants') return jsonResponse({data: tenants})
      if (path === '/admin/models') return jsonResponse({data: models})
      // Anything else is a path the gateway does not serve; the tests below
      // assert the page never asks for one.
      return jsonResponse({error: {message: `unexpected ${path}`}}, 404)
    }),
  )
  return paths
}

function renderCalls() {
  return render(
    <ToastProvider>
      <CallsPage />
    </ToastProvider>,
  )
}

function statCard(label: string): HTMLElement {
  const card = screen.getByText(label).closest('.statCard')
  if (!(card instanceof HTMLElement)) throw new Error(`no stat card for ${label}`)
  return card
}

describe('CallsPage', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })
  afterEach(() => vi.unstubAllGlobals())

  it("renders the status response's daily totals as today's figures", async () => {
    stubFetch()
    renderCalls()

    await waitFor(() => expect(statCard('今日调用').querySelector('.statCardValue')).toHaveTextContent('4,242'))
    expect(statCard('今日调用')).toHaveTextContent('今日消耗 1.2万 Tokens')
    // The key's lifetime counter is shown on its own row, never under the
    // period label on the summary card.
    expect(statCard('今日调用')).not.toHaveTextContent('999,999')
  })

  it('never requests a status path the gateway does not serve', async () => {
    const paths = stubFetch()
    renderCalls()

    await waitFor(() => expect(statCard('今日调用')).toHaveTextContent('4,242'))
    // The page used to ask for /admin/dashboard/status, which the gateway does
    // not route. Only served paths may be requested at all.
    expect(paths.filter(path => path.startsWith('/admin/dashboard'))).toEqual([])
    expect(new Set(paths)).toEqual(
      new Set(['/admin/status', '/admin/api-keys', '/admin/tenants', '/admin/models']),
    )
  })

  it('issues exactly one request per bundle resource on open', async () => {
    const paths = stubFetch()
    renderCalls()

    await waitFor(() => expect(statCard('今日调用')).toHaveTextContent('4,242'))
    // Let any second effect settle before counting.
    await waitFor(() => expect(screen.getByText('校园问答')).toBeInTheDocument())
    for (const path of ['/admin/api-keys', '/admin/tenants', '/admin/models', '/admin/status']) {
      expect(paths.filter(requested => requested === path), `requests to ${path}`).toHaveLength(1)
    }
  })

  it('renders an unavailable state, not a number, when the status source fails', async () => {
    stubFetch({statusFails: true})
    renderCalls()

    await waitFor(() => expect(statCard('今日调用')).toHaveTextContent('今日统计不可用'))
    expect(statCard('今日调用').querySelector('.statCardValue')).toHaveTextContent('—')
    // The key list still renders: only the period figure is unavailable.
    expect(await screen.findByText('校园问答')).toBeInTheDocument()
  })

  it('labels the model card with the quantity it counts: enabled Virtual Models', async () => {
    stubFetch()
    renderCalls()

    // `models` above has one enabled row and one disabled row. The label names
    // exactly what was counted, so the number cannot be read as a permission
    // heuristic.
    await waitFor(() =>
      expect(statCard('启用对外模型').querySelector('.statCardValue')).toHaveTextContent('1'),
    )
    expect(screen.queryByText('受限模型')).not.toBeInTheDocument()
  })

  it('does not refetch anything when switching between the two tabs', async () => {
    const paths = stubFetch()
    renderCalls()

    await waitFor(() => expect(screen.getByText('校园问答')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', {name: '租户配额'}))
    // The tenant view renders the desktop table and the phone cards from the
    // same data; CSS hides one, but both are in the DOM.
    await waitFor(() => expect(screen.getAllByText('校园信息化').length).toBeGreaterThan(0))
    fireEvent.click(screen.getByRole('button', {name: 'API Key'}))
    await waitFor(() => expect(screen.getByText('校园问答')).toBeInTheDocument())

    // Remounting either table must not reload what the page already owns: both
    // views render the page bundle, so switching tabs costs no requests.
    for (const path of ['/admin/api-keys', '/admin/tenants', '/admin/models', '/admin/status']) {
      expect(paths.filter(requested => requested === path), `requests to ${path}`).toHaveLength(1)
    }
  })

  it('formats the expiry column with the shared formatter', async () => {
    stubFetch()
    renderCalls()

    const expected = when(keys[0].expire_at)
    expect(await screen.findByText(expected)).toBeInTheDocument()
  })
})
