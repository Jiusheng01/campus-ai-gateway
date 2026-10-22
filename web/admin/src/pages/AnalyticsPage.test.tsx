import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import {AnalyticsPage} from './AnalyticsPage'
import type {DashboardStatus} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const trend = {
  data: [
    {bucket: '2026-10-20 10:00', requests: 40, tokens: 400, errors: 2, avg_latency_ms: 90, p95_latency_ms: 180},
    {bucket: '2026-10-20 11:00', requests: 60, tokens: 600, errors: 0, avg_latency_ms: 70, p95_latency_ms: 240},
  ],
  bucket: 'hour',
  from: '2026-10-20T10:00:00Z',
  to: '2026-10-22T10:00:00Z',
}

function status(over: Partial<DashboardStatus> = {}): DashboardStatus {
  return {
    gateway: true,
    postgres: true,
    redis: true,
    data_plane: 'direct',
    stats: {today_requests: 100, today_tokens: 1000, today_errors: 4, avg_latency_ms: 80, p95_latency_ms: 260, failover_count: 1},
    provider_stats: [{provider: 'qwen', requests: 60, success_rate: 98.33, avg_latency_ms: 75}],
    ...over,
  }
}

function stub(body: {status?: DashboardStatus | null; trendFails?: boolean; statusFails?: boolean} = {}) {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.startsWith('/admin/status/trend')) return body.trendFails ? jsonResponse({error: {message: '趋势读取失败'}}, 500) : jsonResponse(trend)
    if (url === '/admin/status') {
      if (body.statusFails) return jsonResponse({error: {message: '状态读取失败'}}, 500)
      return jsonResponse(body.status === undefined ? status() : body.status)
    }
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  }))
}

afterEach(() => vi.unstubAllGlobals())

describe('AnalyticsPage channel table', () => {
  it('renders the real per-channel figures with their units', async () => {
    stub()
    render(<AnalyticsPage />)
    expect(await screen.findByText('qwen')).toBeInTheDocument()
    const row = screen.getByText('qwen').closest('tr') as HTMLElement
    expect(row).toHaveTextContent('60')
    expect(row).toHaveTextContent('98.3%')
    expect(row).toHaveTextContent('75 ms')
  })

  it('does not sum provider attempts into the failover figure', async () => {
    stub()
    render(<AnalyticsPage />)
    // The footnote is the status response's own count.
    expect(await screen.findByText(/故障切换 1 次/)).toBeInTheDocument()
  })

  it('says there is no attributable channel data instead of an empty table', async () => {
    stub({status: status({provider_stats: []})})
    render(<AnalyticsPage />)
    expect(await screen.findByText(/暂无可归因的渠道遥测/)).toBeInTheDocument()
  })

  it('reports a failed status read rather than an empty board', async () => {
    stub({statusFails: true})
    render(<AnalyticsPage />)
    expect(await screen.findByText(/状态读取失败/)).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
  })
})

describe('AnalyticsPage trend', () => {
  it('states the bucket width and the time-zone rule for the series', async () => {
    stub()
    render(<AnalyticsPage />)
    expect(await screen.findByText(/按小时聚合/)).toBeInTheDocument()
    expect(screen.getByText(/本机时区/)).toBeInTheDocument()
  })

  it('reports a failed trend read and retries it', async () => {
    stub({trendFails: true})
    render(<AnalyticsPage />)
    expect(await screen.findByText('趋势读取失败')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
  })

  it('does not draw three charts from an empty series', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/admin/status/trend')) return jsonResponse({...trend, data: []})
      if (url === '/admin/status') return jsonResponse(status())
      return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
    }))
    render(<AnalyticsPage />)
    expect(await screen.findAllByText(/该时间范围内没有数据/)).toHaveLength(3)
  })
})

describe('AnalyticsPage latency honesty', () => {
  // The cards and the channel table carry an average; the series carries a P95
  // per bucket. The page must not label one as the other.
  it('keeps the average and the P95 apart', async () => {
    stub()
    render(<AnalyticsPage />)
    const channelTable = (await screen.findByText('qwen')).closest('table') as HTMLElement
    expect(channelTable.textContent).toContain('平均延迟')
    expect(channelTable.textContent).not.toContain('P95')
    // The trend's aggregate readout names the slowest bucket, not a window P95.
    expect(screen.getAllByText(/最慢分桶 P95/).length).toBeGreaterThan(0)
  })
})
