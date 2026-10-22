import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {LogDetail} from './LogDetail'
import {ToastProvider} from '../ui/Toast'
import type {RequestAttempt, RequestLog} from '../../api'

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

const chain: RequestAttempt[] = [
  {attempt_no: 1, provider: 'qwen', upstream_model: 'qwen-flash', outcome: 'failed', status_code: 500, latency_ms: 3, error_kind: 'upstream_5xx'},
  {attempt_no: 2, provider: 'doubao', upstream_model: 'deepseek-v4', outcome: 'served', status_code: 200, latency_ms: 9, error_kind: ''},
]

function stubAttempts(attempts: RequestAttempt[] | number) {
  vi.stubGlobal('fetch', vi.fn(async () => (
    typeof attempts === 'number' ? jsonResponse({error: {message: 'attempts unavailable'}}, attempts) : jsonResponse({data: attempts})
  )))
}

function renderDetail(v: RequestLog) {
  return render(
    <ToastProvider>
      <LogDetail v={v} />
    </ToastProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('LogDetail call chain', () => {
  beforeEach(() => stubAttempts(chain))

  it('shows the alias, the actual channel, the upstream model and the switch state', async () => {
    renderDetail(base)
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
    renderDetail({...base, provider: '', upstream_model: '', failover: false})
    await waitFor(() => expect(screen.getByText('未切换')).toBeInTheDocument())
    const rows = screen.getAllByText('未知')
    expect(rows.length).toBeGreaterThanOrEqual(2)
  })
})

describe('LogDetail attempt timeline', () => {
  // "Failover attempted" and "failover succeeded" are different facts. The
  // first attempt failing is what makes the request a failover; whether the
  // client got an answer depends on what came after it. A chain that failed
  // every attempt must not read as a switch that worked.
  it('separates an attempted failover from a successful one', async () => {
    stubAttempts(chain)
    renderDetail(base)
    await screen.findByText('故障切换成功')

    const timeline = screen.getByRole('region', {name: '尝试链'})
    expect(within(timeline).getByText('第 1 次尝试')).toBeInTheDocument()
    expect(within(timeline).getByText('第 2 次尝试')).toBeInTheDocument()
    // Both the timeline and the attempt table name each outcome; the verdict
    // is asserted above.
    expect(within(timeline).getAllByText('失败').length).toBe(2)
    expect(within(timeline).getAllByText('成功').length).toBeGreaterThan(0)
  })

  it('says the failover attempt did not recover the request when every attempt failed', async () => {
    stubAttempts([
      {attempt_no: 1, provider: 'qwen', upstream_model: 'qwen-flash', outcome: 'failed', status_code: 500, latency_ms: 3, error_kind: 'upstream_5xx'},
      {attempt_no: 2, provider: 'doubao', upstream_model: 'deepseek-v4', outcome: 'failed', status_code: 503, latency_ms: 9, error_kind: 'upstream_5xx'},
    ])
    renderDetail({...base, status_code: 503, failover: true})
    await screen.findByText('故障切换后仍失败')
    const timeline = screen.getByRole('region', {name: '尝试链'})
    expect(within(timeline).getAllByText('失败').length).toBe(4)
    expect(screen.queryByText(/故障切换成功/)).not.toBeInTheDocument()
  })

  it('marks a circuit-skipped line as not attempted rather than failed', async () => {
    stubAttempts([
      {attempt_no: 1, provider: 'qwen', upstream_model: 'qwen-flash', outcome: 'skipped', status_code: 0, latency_ms: 0, error_kind: ''},
      {attempt_no: 2, provider: 'doubao', upstream_model: 'deepseek-v4', outcome: 'served', status_code: 200, latency_ms: 9, error_kind: ''},
    ])
    renderDetail(base)
    const timeline = screen.getByRole('region', {name: '尝试链'})
    expect(await within(timeline).findByText(/熔断跳过/)).toBeInTheDocument()
    expect(within(timeline).getByText(/未发起/)).toBeInTheDocument()
  })

  it('keeps the server order and shows the error classification of each attempt', async () => {
    stubAttempts(chain)
    renderDetail(base)
    const timeline = screen.getByRole('region', {name: '尝试链'})
    await within(timeline).findByText('qwen')
    const rows = within(timeline).getAllByRole('row').slice(1)
    expect(within(rows[0]).getByText('qwen')).toBeInTheDocument()
    expect(within(rows[1]).getByText('doubao')).toBeInTheDocument()
    expect(within(rows[0]).getByText('upstream_5xx')).toBeInTheDocument()
  })

  it('reports a failed attempt read instead of claiming there were no attempts', async () => {
    stubAttempts(500)
    renderDetail(base)
    const timeline = screen.getByRole('region', {name: '尝试链'})
    expect(await within(timeline).findByText(/尝试链读取失败/)).toBeInTheDocument()
    expect(within(timeline).queryByText(/暂无尝试记录/)).not.toBeInTheDocument()
  })
})

describe('LogDetail identifiers', () => {
  it('copies the full request id and reports when the clipboard refuses', async () => {
    stubAttempts(chain)
    const writeText = vi.fn(async () => { throw new Error('denied') })
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText}})
    renderDetail(base)

    await userEvent.click(screen.getByRole('button', {name: /复制 Request ID/}))
    expect(writeText).toHaveBeenCalledWith('req_chain')
    expect(await screen.findByText(/复制失败/)).toBeInTheDocument()
  })

  it('shows the request id in full, not shortened', async () => {
    stubAttempts(chain)
    const long = 'req_0123456789abcdef0123456789abcdef'
    renderDetail({...base, request_id: long})
    const copy = screen.getByRole('button', {name: /复制 Request ID/})
    expect(copy).toHaveAttribute('title', `复制 ${long}`)
    expect(screen.getAllByText(long).length).toBeGreaterThan(0)
  })
})
