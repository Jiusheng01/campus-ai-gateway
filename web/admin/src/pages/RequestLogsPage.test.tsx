import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import {RequestLogsPage} from './RequestLogsPage'
import {ToastProvider} from '../components/ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const emptyTotals = {
  requests: 0,
  errors: 0,
  input_tokens: 0,
  output_tokens: 0,
  total_tokens: 0,
  avg_latency_ms: 0,
  p95_latency_ms: 0,
  failover_count: 0,
  quota: {daily_token: null, monthly_token: null, monthly_request: null},
}

function stubEmptyLogs() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/admin/request-logs/usage')) return jsonResponse({totals: emptyTotals, by_key: null})
      if (url === '/admin/request-logs/facets') return jsonResponse({tenants: null, keys: null})
      if (url.startsWith('/admin/request-logs?')) return jsonResponse({data: null, limit: 50, offset: 0})
      return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
    }),
  )
}

// The server marshals an empty slice as null, so a database with no traffic
// answers with "data": null and "by_key": null. The page must treat both as
// empty rather than crash on them.
describe('admin request logs on an empty database', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the empty state when the server answers with null collections', async () => {
    stubEmptyLogs()
    render(
      <ToastProvider>
        <RequestLogsPage />
      </ToastProvider>,
    )

    expect(await screen.findByText('当前筛选条件下没有请求记录')).toBeInTheDocument()
  })
})
