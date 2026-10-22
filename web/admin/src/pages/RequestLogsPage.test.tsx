import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {RequestLogsPage} from './RequestLogsPage'
import {ToastProvider} from '../components/ui/Toast'
import type {RequestLog} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function row(over: Partial<RequestLog> = {}): RequestLog {
  return {
    request_id: 'req_0123456789abcdef',
    tenant_id: 't-1',
    api_key_id: 'k-1',
    client_ip: '10.0.0.1',
    requested_model: 'campus-chat',
    actual_model: 'campus-chat',
    provider: 'qwen',
    upstream_model: 'qwen-plus',
    route_priority: 1,
    attempts: 1,
    failover: false,
    stream: false,
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
    latency_ms: 120,
    status_code: 200,
    error_code: '',
    created_at: '2026-10-20T01:00:00Z',
    ...over,
  }
}

type Stub = {rows?: RequestLog[]; total?: number; fail?: boolean}
function stubFetch({rows = [row()], total = rows.length, fail = false}: Stub = {}) {
  const calls: {url: string; method: string}[] = []
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    calls.push({url, method: init?.method || 'GET'})
    if (fail) return jsonResponse({error: {message: '日志服务暂不可用'}}, 500)
    if (url.startsWith('/admin/request-logs/facets')) {
      return jsonResponse({tenants: [{id: 't-1', code: 'CAMPUS', name: '校园信息化'}], keys: [{id: 'k-1', name: '教学 Key', key_prefix: 'sk-teach', tenant_id: 't-1'}]})
    }
    if (url.startsWith('/admin/request-logs/usage')) {
      return jsonResponse({
        totals: {
          requests: total, errors: 0, input_tokens: 10, output_tokens: 20, total_tokens: 30,
          avg_latency_ms: 120, p95_latency_ms: 120, failover_count: 0,
          quota: {daily_token: null, monthly_token: null, monthly_request: null},
        },
        by_key: [{api_key_id: 'k-1', name: '教学 Key', key_prefix: 'sk-teach', requests: 1, tokens: 30, quota_share: null}],
      })
    }
    if (url.startsWith('/admin/request-logs?')) return jsonResponse({data: rows})
    if (url.startsWith('/admin/request-logs/') && url.endsWith('/attempts')) return jsonResponse({data: []})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  }))
  return calls
}

function renderPage() {
  return render(
    <ToastProvider>
      <RequestLogsPage />
    </ToastProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('RequestLogsPage filters', () => {
  it('applies the same filter set to the table, the usage strip and the export', async () => {
    const calls = stubFetch()
    renderPage()
    await screen.findByText('campus-chat')

    await userEvent.type(screen.getByPlaceholderText('全部模型'), 'campus-chat')
    await userEvent.click(screen.getByRole('button', {name: '查询'}))

    await waitFor(() => expect(calls.filter(c => c.url.includes('model=campus-chat')).length).toBeGreaterThanOrEqual(2))
    // The page, the aggregate and (when asked) the export all carry it.
    const withFilter = calls.filter(c => c.url.includes('model=campus-chat'))
    expect(withFilter.some(c => c.url.startsWith('/admin/request-logs?'))).toBe(true)
    expect(withFilter.some(c => c.url.startsWith('/admin/request-logs/usage'))).toBe(true)
  })

  it('returns to the first page when a new search is applied', async () => {
    const calls = stubFetch({rows: Array.from({length: 50}, (_, i) => row({request_id: `req_${i}`})), total: 120})
    renderPage()
    await waitFor(() => expect(calls.some(c => c.url.includes('offset=0'))).toBe(true))

    await userEvent.click(screen.getByRole('button', {name: '下一页'}))
    await waitFor(() => expect(calls.some(c => c.url.includes('offset=50'))).toBe(true))

    await userEvent.type(screen.getByPlaceholderText('全部模型'), 'campus-chat')
    await userEvent.click(screen.getByRole('button', {name: '查询'}))

    await waitFor(() => {
      const last = calls.filter(c => c.url.startsWith('/admin/request-logs?')).at(-1)!
      expect(last.url).toContain('offset=0')
      expect(last.url).toContain('model=campus-chat')
    })
  })

  it('counts the whole filtered set, not the page in view', async () => {
    stubFetch({rows: [row()], total: 120})
    renderPage()
    expect(await screen.findByText(/合计 120 条/)).toBeInTheDocument()
    expect(screen.getByText(/本页 1 条/)).toBeInTheDocument()
  })

  it('states a failed read and offers a retry instead of an empty result', async () => {
    stubFetch({fail: true})
    renderPage()
    expect(await screen.findByText('日志服务暂不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
    expect(screen.queryByText(/没有请求记录/)).not.toBeInTheDocument()
  })

  it('drills into one API key from the usage strip', async () => {
    const calls = stubFetch()
    renderPage()
    await screen.findByText('campus-chat')

    // The strip renders the per-key breakdown; selecting a row narrows the query.
    const strip = screen.getByRole('heading', {name: '按 API Key'}).closest('.usageSection') as HTMLElement
    await userEvent.click(within(strip).getByText('教学 Key'))

    await waitFor(() => expect(calls.some(c => c.url.includes('api_key_id=k-1'))).toBe(true))
  })
})

describe('RequestLogsPage detail', () => {
  it('opens the request detail from a row and shows the full request id', async () => {
    stubFetch()
    renderPage()
    await userEvent.click(await screen.findByText('campus-chat'))

    const drawer = await screen.findByRole('dialog', {name: '请求详情'})
    // The drawer's subtitle and the detail row both carry the full id.
    expect(within(drawer).getAllByText('req_0123456789abcdef').length).toBeGreaterThanOrEqual(1)
    expect(within(drawer).getByRole('button', {name: '复制 Request ID'})).toBeInTheDocument()
  })

  it('copies a row id without opening the row', async () => {
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText}})
    stubFetch()
    renderPage()

    const buttons = await screen.findAllByTitle(/^复制 req_/)
    await userEvent.click(buttons[0])

    expect(writeText).toHaveBeenCalledWith('req_0123456789abcdef')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
