import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest'
import {render, screen, waitFor} from '@testing-library/react'
import {DashboardPage} from './DashboardPage'
import {ToastProvider} from '../components/ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const status = {
  gateway: true, postgres: true, redis: true, data_plane: 'direct',
  stats: {today_requests: 0, today_tokens: 0, today_errors: 0, avg_latency_ms: 0, p95_latency_ms: 0, failover_count: 0},
  provider_stats: [],
  config_counts: {models: 0, enabled_models: 0, providers: 0, enabled_providers: 0},
  route_cache: {built_at: null, stale: false, generation: 0},
}

describe('DashboardPage on a fresh deployment', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/admin/status')) return jsonResponse(status)
      // A fresh database has no request rows; the API sends data: null.
      if (url.startsWith('/admin/request-logs')) return jsonResponse({data: null})
      return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('renders the empty state instead of crashing when the log is empty', async () => {
    render(
      <ToastProvider>
        <DashboardPage roles={['super_admin']} />
      </ToastProvider>,
    )
    expect(await screen.findByText('配置概况')).toBeInTheDocument()
    expect(await screen.findByText('暂无调用记录')).toBeInTheDocument()
  })
})
