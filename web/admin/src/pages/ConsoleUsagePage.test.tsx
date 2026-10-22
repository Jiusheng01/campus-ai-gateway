import {afterEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {ConsoleUsagePage} from './ConsoleUsagePage'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const usage = {
  from: '2026-10-20T00:00:00Z',
  to: '2026-10-21T00:00:00Z',
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
    {bucket: '2026-10-20 10:00', requests: 2, tokens: 30, errors: 0, avg_latency_ms: 10, p95_latency_ms: 20},
    {bucket: '2026-10-20 11:00', requests: 1, tokens: 15, errors: 1, avg_latency_ms: 10, p95_latency_ms: 20},
  ],
  by_model: [
    {alias: 'campus-chat', requests: 2, input_tokens: 30, output_tokens: 5, total_tokens: 35},
    {alias: 'campus-embed', requests: 1, input_tokens: 7, output_tokens: 3, total_tokens: 10},
  ],
  tenant_quota: {
    tenant_id: 't-1',
    tenant_name: '校园信息化',
    daily_token: {used: 345, limit: 1000, pct: 34.5},
    monthly_token: {used: 345, limit: 10000, pct: 3.45},
    monthly_request: {used: 5, limit: 20, pct: 25},
  },
  cost_available: false,
}

function stubUsage(body: unknown = usage) {
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input))
      return jsonResponse(body)
    }),
  )
  return calls
}

/** The value inside the stat card whose label is `label`. */
function statCard(label: string): HTMLElement {
  const labelEl = screen.getByText(label, {selector: '.statCardLabel'})
  return labelEl.closest('.statCard') as HTMLElement
}

function query(url: string): URLSearchParams {
  return new URL(url, 'http://gateway.local').searchParams
}

describe('personal usage page', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the returned totals, per-model rows and the shared quota without a currency figure', async () => {
    stubUsage()
    render(<ConsoleUsagePage />)

    // Totals: the seven figures the endpoint returned, including the split
    // input/output/total tokens and the success/failure counts.
    expect(await screen.findByText('用量概览')).toBeInTheDocument()
    expect(within(statCard('请求数')).getByText('3')).toBeInTheDocument()
    expect(within(statCard('成功')).getByText('2')).toBeInTheDocument()
    expect(within(statCard('失败')).getByText('1')).toBeInTheDocument()
    expect(within(statCard('错误率')).getByText('33.33%')).toBeInTheDocument()
    expect(within(statCard('输入 Token')).getByText('37')).toBeInTheDocument()
    expect(within(statCard('输出 Token')).getByText('8')).toBeInTheDocument()
    expect(within(statCard('总 Token')).getByText('45')).toBeInTheDocument()

    // The per-model table is the real aggregate, not a placeholder row.
    const table = screen.getByRole('table')
    expect(within(table).getByText('campus-chat')).toBeInTheDocument()
    expect(within(table).getByText('campus-embed')).toBeInTheDocument()
    expect(within(table).getByText('35')).toBeInTheDocument()
    expect(within(table).getByText('10')).toBeInTheDocument()

    // The shared quota is presented as an aggregate read-only share.
    expect(screen.getByText('校园信息化')).toBeInTheDocument()
    expect(screen.getByText('34.50%')).toBeInTheDocument()
    expect(screen.getByText('345 / 1,000')).toBeInTheDocument()
    expect(screen.getByText('25.00%')).toBeInTheDocument()

    // Cost is explicitly unavailable.
    expect(screen.getByText(/费用暂不可用/)).toBeInTheDocument()

    // No derived currency anywhere on the page.
    const text = document.body.textContent || ''
    for (const symbol of ['¥', '￥', '$', '元']) {
      expect(text).not.toContain(symbol)
    }
  })

  it('changes the requested window when the period selector changes', async () => {
    const calls = stubUsage()
    render(<ConsoleUsagePage />)

    await screen.findByText('用量概览')
    const first = query(calls[0])
    expect(first.get('bucket')).toBe('hour')
    const firstFrom = Date.parse(first.get('from') || '')
    const firstTo = Date.parse(first.get('to') || '')
    expect(firstTo - firstFrom).toBe(24 * 3600 * 1000)

    fireEvent.click(screen.getByRole('button', {name: '近 30 天'}))

    await waitFor(() => expect(calls.length).toBeGreaterThan(1))
    const last = query(calls[calls.length - 1])
    expect(last.get('bucket')).toBe('day')
    const from = Date.parse(last.get('from') || '')
    const to = Date.parse(last.get('to') || '')
    expect(to - from).toBe(30 * 24 * 3600 * 1000)
  })

  it('states that an unassigned account has no shared quota', async () => {
    stubUsage({...usage, tenant_quota: null})
    render(<ConsoleUsagePage />)

    expect(await screen.findByText('尚未分配租户')).toBeInTheDocument()
    expect(screen.queryByText('租户共享配额')).toBeInTheDocument()
  })
})

describe('personal usage quota honesty', () => {
  afterEach(() => vi.unstubAllGlobals())

  // The shared quota is enforced on UTC days and months while the personal
  // window above it uses the reader's time zone. Saying which is which stops
  // the two looking contradictory.
  it('names the UTC boundary of the shared quota separately from the personal window', async () => {
    stubUsage()
    render(<ConsoleUsagePage />)
    await screen.findByText('租户共享配额')
    const panel = screen.getByText('租户共享配额').closest('.panel') as HTMLElement
    expect(within(panel).getByText(/UTC/)).toBeInTheDocument()
    expect(within(panel).getAllByText(/共享/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/本机时区/).length).toBeGreaterThan(0)
  })

  it('states the overage when a shared band is past its limit', async () => {
    stubUsage({...usage, tenant_quota: {...usage.tenant_quota, daily_token: {used: 1250, limit: 1000, pct: 125}}})
    render(<ConsoleUsagePage />)
    await screen.findByText('租户共享配额')
    const panel = screen.getByText('租户共享配额').closest('.panel') as HTMLElement
    expect(within(panel).getByText(/已超出 250/)).toBeInTheDocument()
    expect(within(panel).getByText(/125\.00%/)).toBeInTheDocument()
  })

  it('does not draw a quota band as 0% when there is no configured limit', async () => {
    stubUsage({...usage, tenant_quota: {...usage.tenant_quota, daily_token: null}})
    render(<ConsoleUsagePage />)
    await screen.findByText('租户共享配额')
    const panel = screen.getByText('租户共享配额').closest('.panel') as HTMLElement
    expect(within(panel).getByText('未设')).toBeInTheDocument()
    expect(within(panel).queryByText('0.00%')).not.toBeInTheDocument()
  })

  it('does not draw a trend from an empty series but still reports the window', async () => {
    stubUsage({...usage, totals: {...usage.totals, requests: 0, successes: 0, failures: 0, total_tokens: 0, error_rate: 0}, series: [], by_model: []})
    render(<ConsoleUsagePage />)
    expect(await screen.findAllByText(/该时间范围内没有数据/)).toHaveLength(2)
    expect(screen.getByText(/所选时间段内没有可归因的请求/)).toBeInTheDocument()
    // Zero traffic is a measurement here, not an error: the cards stay.
    expect(statCard('请求数')).toHaveTextContent('0')
  })

  it('reports a failed read instead of showing zero usage', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({error: {message: '用量服务不可用'}}, 500)))
    render(<ConsoleUsagePage />)
    expect(await screen.findByText('用量服务不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
    expect(screen.queryByText('请求数', {selector: '.statCardLabel'})).not.toBeInTheDocument()
  })
})
