import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {AuditPage} from './AuditPage'
import {SecurityPage} from './SecurityPage'
import {ToastProvider} from '../components/ui/Toast'
import type {AdminAudit, SecurityEvent} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const audits: AdminAudit[] = [
  {id: 1, request_id: 'req_0123456789abcdef', actor: 'zhangsan', action: 'provider.update', resource: 'provider/campus-qa-openai', status_code: 200, client_ip: '10.0.0.1', created_at: '2026-10-20T08:00:00Z'},
  {id: 2, request_id: 'req_fedcba9876543210', actor: 'lisi', action: 'user.roles', resource: 'user/u-2', status_code: 403, client_ip: '10.0.0.2', created_at: '2026-10-20T09:00:00Z'},
]

const events: SecurityEvent[] = [
  {id: 1, request_id: 'req_aaaabbbbccccdddd', tenant_id: 't-1', api_key_id: 'k-1', event_type: 'quota_denied', client_ip: '10.0.0.1', requested_model: 'campus-chat', detail: 'daily token limit reached', created_at: '2026-10-20T08:00:00Z'},
  {id: 2, request_id: 'req_eeeeffff00001111', tenant_id: 't-2', api_key_id: 'k-2', event_type: 'auth_failed', client_ip: '10.0.0.9', requested_model: 'campus-chat', detail: 'invalid key', created_at: '2026-10-20T09:00:00Z'},
]

function stub(kind: 'audit' | 'security', opts: {fail?: boolean} = {}) {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (opts.fail) return jsonResponse({error: {message: '记录服务暂不可用'}}, 500)
    if (kind === 'audit' && url.startsWith('/admin/admin-audit')) return jsonResponse({data: audits})
    if (kind === 'security' && url.startsWith('/admin/security-events')) return jsonResponse({data: events})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  }))
}

const wrap = (node: React.ReactNode) => render(<ToastProvider>{node}</ToastProvider>)

afterEach(() => vi.unstubAllGlobals())

describe('AuditPage records', () => {
  it('names the capped window the list was read over', async () => {
    stub('audit')
    wrap(<AuditPage />)
    expect(await screen.findByText(/最近 200 条/)).toBeInTheDocument()
    expect(screen.getByText(/筛选只作用于这/)).toBeInTheDocument()
  })

  it('shows each record with its actor, action, object and status', async () => {
    stub('audit')
    wrap(<AuditPage />)
    const row = (await screen.findByText('zhangsan')).closest('tr') as HTMLElement
    expect(row).toHaveTextContent('provider/campus-qa-openai')
    expect(row).toHaveTextContent('200')
    expect(row).toHaveTextContent('10.0.0.1')
  })

  it('filters by actor, object and status within the loaded window', async () => {
    stub('audit')
    wrap(<AuditPage />)
    await screen.findByText('zhangsan')

    await userEvent.type(screen.getByPlaceholderText('搜索管理员、资源或 Request ID...'), 'lisi')
    await waitFor(() => expect(screen.queryByText('zhangsan')).not.toBeInTheDocument())
    expect(screen.getByText('lisi')).toBeInTheDocument()

    await userEvent.clear(screen.getByPlaceholderText('搜索管理员、资源或 Request ID...'))
    await userEvent.selectOptions(screen.getByLabelText('状态'), '403')
    await waitFor(() => expect(screen.queryByText('zhangsan')).not.toBeInTheDocument())
    expect(screen.getByText('lisi')).toBeInTheDocument()
  })

  it('states the filtered count against the loaded window', async () => {
    stub('audit')
    wrap(<AuditPage />)
    await screen.findByText('zhangsan')
    expect(screen.getByText(/2 条记录/)).toBeInTheDocument()
    await userEvent.type(screen.getByPlaceholderText('搜索管理员、资源或 Request ID...'), 'zhangsan')
    await waitFor(() => expect(screen.getByText(/筛选后 1 条（共读取 2 条）/)).toBeInTheDocument())
  })

  it('opens a record detail with the full Request ID and copies it', async () => {
    stub('audit')
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText}})
    wrap(<AuditPage />)

    await userEvent.click(await screen.findByText('zhangsan'))
    const drawer = await screen.findByRole('dialog', {name: '审计记录详情'})
    expect(within(drawer).getByText('req_0123456789abcdef')).toBeInTheDocument()

    await userEvent.click(within(drawer).getByRole('button', {name: '复制 Request ID'}))
    expect(writeText).toHaveBeenCalledWith('req_0123456789abcdef')
  })

  it('reports a failed read and retries it', async () => {
    stub('audit', {fail: true})
    wrap(<AuditPage />)
    expect(await screen.findByText('记录服务暂不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
  })

  it('says the filters matched nothing instead of claiming there are no records', async () => {
    stub('audit')
    wrap(<AuditPage />)
    await screen.findByText('zhangsan')
    await userEvent.type(screen.getByPlaceholderText('搜索管理员、资源或 Request ID...'), 'nobody')
    expect(await screen.findByText(/没有符合条件的记录/)).toBeInTheDocument()
    expect(screen.queryByText(/暂无审计日志记录/)).not.toBeInTheDocument()
  })
})

describe('SecurityPage records', () => {
  it('names the capped window and states the filter applies to it', async () => {
    stub('security')
    wrap(<SecurityPage />)
    expect(await screen.findByText(/最近 200 条/)).toBeInTheDocument()
    expect(screen.getByText(/筛选只作用于这/)).toBeInTheDocument()
  })

  it('filters by event type and by model', async () => {
    stub('security')
    wrap(<SecurityPage />)
    await screen.findByText('daily token limit reached')

    await userEvent.selectOptions(screen.getByLabelText('事件类型'), 'auth_failed')
    await waitFor(() => expect(screen.queryByText('daily token limit reached')).not.toBeInTheDocument())
    expect(screen.getByText('invalid key')).toBeInTheDocument()
  })

  it('opens the detail with the full Request ID, source facts and a copy action', async () => {
    stub('security')
    wrap(<SecurityPage />)
    await userEvent.click(await screen.findByText('daily token limit reached'))

    const drawer = await screen.findByRole('dialog', {name: '安全事件详情'})
    expect(within(drawer).getByText('req_aaaabbbbccccdddd')).toBeInTheDocument()
    expect(within(drawer).getByRole('button', {name: '复制 Request ID'})).toBeInTheDocument()
    expect(within(drawer).getByText('t-1')).toBeInTheDocument()
    expect(within(drawer).getByText('k-1')).toBeInTheDocument()
  })

  it('reports a failed read and retries it', async () => {
    stub('security', {fail: true})
    wrap(<SecurityPage />)
    expect(await screen.findByText('记录服务暂不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
  })
})
