import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {RuntimeSummaryPanel} from './RuntimeSummaryPanel'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

describe('RuntimeSummaryPanel', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({
      alias: 'demo',
      from: '2026-10-20T00:00:00Z',
      to: '2026-10-21T00:00:00Z',
      totals: {requests: 2, errors: 1, success_rate: 50, p95_latency_ms: 12, failover: 1, tokens: 20},
      recent: [{
        request_id: 'req-1', created_at: '2026-10-20T01:00:00Z', requested_model: 'demo',
        actual_model: 'up', provider: 'Qwen', upstream_model: 'up', route_priority: 1,
        status_code: 502, error_code: 'upstream_error', attempts: 1, failover: false,
        total_tokens: 10, latency_ms: 12,
        // Extra fields a future server might accidentally add must not be rendered.
        tenant_id: 'tenant-secret', api_key_id: 'key-secret', client_ip: '10.0.0.1',
      }],
    })))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('does not offer unfiltered raw logs while the summary is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({error: {message: 'summary unavailable'}}, 503)))
    render(<RuntimeSummaryPanel kind="provider" id="p-1" canOpenRawLogs onOpenLogs={() => {}} />)
    expect(screen.getByRole('button', {name: '打开原始调用日志'})).toBeDisabled()
    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '打开原始调用日志'})).toBeDisabled()
  })

  it('renders only the redacted summary fields and supports raw-log navigation', async () => {
    const openLogs = vi.fn()
    render(<RuntimeSummaryPanel kind="model" id="m-1" canOpenRawLogs onOpenLogs={openLogs} />)
    expect(await screen.findByText('req-1')).toBeInTheDocument()
    expect(screen.queryByText('tenant-secret')).not.toBeInTheDocument()
    expect(screen.queryByText('key-secret')).not.toBeInTheDocument()
    expect(screen.queryByText('10.0.0.1')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: '打开原始调用日志'}))
    expect(openLogs).toHaveBeenCalledWith({model: 'demo'})
  })
})
