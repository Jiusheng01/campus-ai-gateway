import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest'
import {render, screen, waitFor} from '@testing-library/react'
import {LogDetail} from './LogDetail'
import type {RequestLog} from '../../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const base: RequestLog = {
  request_id: 'req_chain', tenant_id: 't-1', api_key_id: 'k-1', client_ip: '10.0.0.1',
  requested_model: 'campus-smart', actual_model: 'upstream-b', provider: 'doubao',
  upstream_model: 'deepseek-v4', route_priority: 2, attempts: 2, failover: true,
  stream: false, input_tokens: 2, output_tokens: 3, total_tokens: 5,
  latency_ms: 12, status_code: 200, error_code: '', created_at: '2026-10-20T01:00:00Z',
}

describe('LogDetail call chain', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: [
      {attempt_no: 1, provider: 'qwen', upstream_model: 'qwen-flash', outcome: 'failed', status_code: 500, latency_ms: 3, error_kind: ''},
      {attempt_no: 2, provider: 'doubao', upstream_model: 'deepseek-v4', outcome: 'served', status_code: 200, latency_ms: 9, error_kind: ''},
    ]})))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('shows the alias, the actual channel, the upstream model and the switch state', async () => {
    render(<LogDetail v={base} />)
    // The attempt chain is fetched on open; wait for it so the assertion does
    // not run against a mid-update render.
    await waitFor(() => expect(screen.getAllByText('deepseek-v4').length).toBeGreaterThan(0))
    expect(screen.getByText('调用链路')).toBeInTheDocument()
    expect(screen.getAllByText('campus-smart').length).toBeGreaterThan(0)
    expect(screen.getAllByText('doubao').length).toBeGreaterThan(0)
    expect(screen.getAllByText('deepseek-v4').length).toBeGreaterThan(0)
    expect(screen.getByText('已故障切换')).toBeInTheDocument()
    expect(screen.queryByText('未切换')).not.toBeInTheDocument()
  })

  it('does not invent a channel when telemetry is absent', async () => {
    render(<LogDetail v={{...base, provider: '', upstream_model: '', failover: false}} />)
    await waitFor(() => expect(screen.getByText('未切换')).toBeInTheDocument())
    const rows = screen.getAllByText('未知')
    expect(rows.length).toBeGreaterThanOrEqual(2)
  })
})
