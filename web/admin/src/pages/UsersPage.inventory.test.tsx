import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {UsersPage} from './UsersPage'
import {ToastProvider} from '../components/ui/Toast'
import type {AccountUser, Tenant} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const tenants = [{id: 't-1', code: 'cs-dept', name: '计算机学院', status: 1, daily_token_limit: 0, monthly_token_limit: 0, monthly_request_limit: 0, monthly_budget_micros: 0, rpm_limit: 0, tpm_limit: 0, max_concurrency: 0}] as Tenant[]

function user(over: Partial<AccountUser> = {}): AccountUser {
  return {
    id: 'u-1',
    username: 'zhangsan',
    display_name: '张三',
    roles: ['member'],
    status: 1,
    tenant_id: 't-1',
    tenant_name: '计算机学院',
    password_reset_required: false,
    last_login_at: '2026-10-20T08:00:00Z',
    created_at: '2026-01-01T00:00:00Z',
    ...over,
  }
}

const users = [
  user(),
  user({id: 'u-2', username: 'lisi', display_name: '李四', roles: ['gateway_admin'], tenant_id: '', tenant_name: ''}),
  user({id: 'u-3', username: 'wangwu', display_name: '王五', status: 2}),
]

function stub(body: {fail?: boolean; pending?: boolean; users?: AccountUser[]; stats?: Record<string, number>} = {}) {
  const stats = body.stats ?? {total: 3, members: 1, admins: 1, disabled: 1}
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url === '/admin/users') {
      if (body.pending) return new Promise<Response>(() => {})
      if (body.fail) return jsonResponse({error: {message: '账号服务暂不可用'}}, 500)
      return jsonResponse({data: body.users ?? users, stats})
    }
    if (url === '/admin/tenants') return jsonResponse({data: tenants})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  }))
}

function renderPage() {
  return render(
    <ToastProvider>
      <UsersPage />
    </ToastProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('UsersPage summary honesty', () => {
  // Zero is a count. Before the list answers, or after it fails, there is no
  // count, and printing 0 states an empty account table that was never read.
  it('does not print zero counts while the list is loading', async () => {
    stub({pending: true})
    renderPage()
    const total = screen.getByText('总用户').closest('.kpi') as HTMLElement
    expect(total).toHaveTextContent('—')
    expect(total).not.toHaveTextContent('0')
  })

  it('does not print zero counts when the list failed, and offers a retry', async () => {
    stub({fail: true})
    renderPage()
    const total = screen.getByText('总用户').closest('.kpi') as HTMLElement
    expect(total).toHaveTextContent('—')
    expect(await screen.findByRole('button', {name: '重新加载'})).toBeInTheDocument()
  })

  it('prints the counts the server reported once loaded', async () => {
    stub()
    renderPage()
    await screen.findByText('zhangsan')
    expect((screen.getByText('总用户').closest('.kpi') as HTMLElement)).toHaveTextContent('3')
    expect((screen.getByText('管理员').closest('.kpi') as HTMLElement)).toHaveTextContent('1')
    expect((screen.getByText('停用账号').closest('.kpi') as HTMLElement)).toHaveTextContent('1')
  })
})

describe('UsersPage search and filters', () => {
  it('narrows by username, display name and tenant as the operator types', async () => {
    stub()
    renderPage()
    await screen.findByText('zhangsan')

    const search = screen.getByPlaceholderText('搜索用户名、显示名称或租户...')
    await userEvent.type(search, '李四')
    await waitFor(() => expect(screen.queryByText('zhangsan')).not.toBeInTheDocument())
    expect(screen.getByText('lisi')).toBeInTheDocument()

    await userEvent.clear(search)
    await userEvent.type(search, '计算机学院')
    await waitFor(() => expect(screen.queryByText('lisi')).not.toBeInTheDocument())
    expect(screen.getByText('zhangsan')).toBeInTheDocument()
  })

  it('filters by role and by status', async () => {
    stub()
    renderPage()
    await screen.findByText('zhangsan')

    await userEvent.selectOptions(screen.getByLabelText('角色'), 'gateway_admin')
    await waitFor(() => expect(screen.queryByText('zhangsan')).not.toBeInTheDocument())
    expect(screen.getByText('lisi')).toBeInTheDocument()

    await userEvent.selectOptions(screen.getByLabelText('角色'), '')
    await userEvent.selectOptions(screen.getByLabelText('状态'), '2')
    await waitFor(() => expect(screen.queryByText('zhangsan')).not.toBeInTheDocument())
    expect(screen.getByText('wangwu')).toBeInTheDocument()
  })

  it('says the filters matched nothing instead of showing the empty-table message', async () => {
    stub()
    renderPage()
    await screen.findByText('zhangsan')

    await userEvent.type(screen.getByPlaceholderText('搜索用户名、显示名称或租户...'), 'nobody-matches')
    expect(await screen.findByText(/没有符合条件的账号/)).toBeInTheDocument()
    expect(screen.queryByText(/开放注册后/)).not.toBeInTheDocument()
  })

  it('states the row count of the filtered view', async () => {
    stub()
    renderPage()
    await screen.findByText('zhangsan')
    expect(screen.getByText(/3 个账号/)).toBeInTheDocument()
    await userEvent.type(screen.getByPlaceholderText('搜索用户名、显示名称或租户...'), 'zhangsan')
    await waitFor(() => expect(screen.getByText(/1 个账号/)).toBeInTheDocument())
  })
})

describe('UsersPage tenant facts', () => {
  // A Tenant id with no name is "the gateway did not say", not "unassigned":
  // 未分配 would claim the account has no membership, which is the opposite.
  it('does not report a named-but-missing tenant as 未分配', async () => {
    stub({users: [user({tenant_id: 't-9', tenant_name: ''})]})
    renderPage()
    const row = (await screen.findByText('zhangsan')).closest('tr') as HTMLElement
    expect(within(row).getByText('租户名称未返回')).toBeInTheDocument()
    expect(row).not.toHaveTextContent('未分配')
  })

  it('reports a genuinely unassigned account as 未分配', async () => {
    stub({users: [user({tenant_id: '', tenant_name: ''})]})
    renderPage()
    const row = (await screen.findByText('zhangsan')).closest('tr') as HTMLElement
    expect(within(row).getByText('未分配')).toBeInTheDocument()
  })
})
