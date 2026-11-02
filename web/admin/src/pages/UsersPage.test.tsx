import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {UsersPage} from './UsersPage'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import {ToastProvider} from '../components/ui/Toast'
import type {AccountUser, Tenant} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const tenants = [
  {
    id: 't-1',
    code: 'cs-dept',
    name: '计算机学院',
    status: 1,
    daily_token_limit: 0,
    monthly_token_limit: 0,
    monthly_request_limit: 0,
    monthly_budget_micros: 0,
    rpm_limit: 0,
    tpm_limit: 0,
    max_concurrency: 0,
  },
] as Tenant[]

const users = [
  {
    id: 'u-unassigned',
    username: 'zhangsan',
    display_name: '张三',
    roles: ['member'],
    status: 1,
    tenant_id: '',
    tenant_name: '',
    last_login_at: null,
    created_at: '2026-01-01T00:00:00Z',
  },
  {
    id: 'u-assigned',
    username: 'lisi',
    display_name: '李四',
    roles: ['member'],
    status: 1,
    tenant_id: 't-1',
    tenant_name: '计算机学院',
    last_login_at: null,
    created_at: '2026-01-02T00:00:00Z',
  },
] as AccountUser[]

function stubUsersAPI(putResponse: () => Response = () => jsonResponse({data: {...users[0], tenant_id: 't-1', tenant_name: '计算机学院'}})) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === '/admin/users' && (!init?.method || init.method === 'GET')) {
      return jsonResponse({data: users, stats: {total: 2, members: 2, admins: 0, disabled: 0}})
    }
    if (url === '/admin/tenants') return jsonResponse({data: tenants})
    if (url === '/admin/users/u-unassigned/tenant' && init?.method === 'PUT') return putResponse()
    return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderUsersPage() {
  return render(
    <ToastProvider>
      <UsersPage />
    </ToastProvider>,
  )
}

async function openAssignDrawer(user: ReturnType<typeof userEvent.setup>) {
  const row = (await screen.findByText('zhangsan')).closest('tr') as HTMLElement
  await user.click(within(row).getByRole('button', {name: /管理/}))
  return screen.findByRole('dialog', {name: '管理账号'})
}

describe('UsersPage Tenant Membership', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders each account Tenant name, or 未分配 when it has none', async () => {
    stubUsersAPI()
    renderUsersPage()

    const unassignedRow = (await screen.findByText('zhangsan')).closest('tr') as HTMLElement
    const assignedRow = screen.getByText('lisi').closest('tr') as HTMLElement

    expect(unassignedRow).toHaveTextContent('未分配')
    expect(assignedRow).toHaveTextContent('计算机学院')
  })

  it('assigns a Tenant from the edit drawer and sends only the Tenant change', async () => {
    const fetchMock = stubUsersAPI()
    const user = userEvent.setup()
    renderUsersPage()

    const dialog = await openAssignDrawer(user)
    const tenantSelect = within(dialog).getAllByRole('combobox')[0]
    fireEvent.change(tenantSelect, {target: {value: 't-1'}})
    await user.click(within(dialog).getByRole('button', {name: '保存更改'}))

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([url, init]) => String(url) === '/admin/users/u-unassigned/tenant' && init?.method === 'PUT',
      )
      expect(call).toBeTruthy()
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({tenant_id: 't-1'})
    })
    // No role, status or display-name edit was made, so the account patch must
    // not be sent — it would otherwise sign the account out for no reason.
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) => String(url) === '/admin/users/u-unassigned' && init?.method === 'PATCH',
      ),
    ).toBe(false)
  })

  it('surfaces the owned-key count when the gateway refuses the assignment', async () => {
    stubUsersAPI(() =>
      jsonResponse(
        {
          error: {
            code: 'user_owns_keys',
            message: 'the account still owns API keys',
            owned_key_count: 3,
          },
        },
        409,
      ),
    )
    const user = userEvent.setup()
    renderUsersPage()

    const dialog = await openAssignDrawer(user)
    const tenantSelect = within(dialog).getAllByRole('combobox')[0]
    fireEvent.change(tenantSelect, {target: {value: 't-1'}})
    await user.click(within(dialog).getByRole('button', {name: '保存更改'}))

    expect(await screen.findByText(/3 个 API Key/)).toBeInTheDocument()
  })
})

// The reset is the only recovery path that does not depend on the account
// remembering anything, so the operator has to be able to reach it, and has to
// be told what it costs the account (every session, and a forced password
// change) before it happens.
describe('UsersPage password reset', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  // The page is rendered with a ConfirmProvider here: useConfirm's default
  // resolves false, so a test without one would prove only that the dialog is
  // skipped — not that a confirmed reset reaches the gateway.
  function renderWithConfirm() {
    return render(
      <ToastProvider>
        <ConfirmProvider>
          <UsersPage />
        </ConfirmProvider>
      </ToastProvider>,
    )
  }

  function stubWithReset(onReset: () => Response = () => jsonResponse({data: {...users[0], password_reset_required: true}})) {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/admin/users' && (!init?.method || init.method === 'GET')) {
        return jsonResponse({data: users, stats: {total: 2, members: 2, admins: 0, disabled: 0}})
      }
      if (url === '/admin/tenants') return jsonResponse({data: tenants})
      if (url === '/admin/users/u-unassigned/password-reset' && init?.method === 'POST') return onReset()
      return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  it('sends the typed password to the reset endpoint', async () => {
    const fetchMock = stubWithReset()
    const user = userEvent.setup()
    renderWithConfirm()

    const dialog = await openAssignDrawer(user)
    const input = within(dialog).getByPlaceholderText('至少 10 位，需包含两类字符')
    await user.type(input, 'Interim#2026Reset')
    await user.click(within(dialog).getByRole('button', {name: '重置密码'}))

    // The confirmation is not decoration: the action signs the account out
    // everywhere, so it must not fire from a single click.
    const confirmDialog = await screen.findByRole('dialog', {name: /重置 zhangsan 的密码/})
    await user.click(within(confirmDialog).getByRole('button', {name: '重置密码'}))

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([url, init]) => String(url) === '/admin/users/u-unassigned/password-reset' && init?.method === 'POST',
      )
      expect(call).toBeTruthy()
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({new_password: 'Interim#2026Reset'})
    })
  })

  it('shows that a handover is still outstanding', async () => {
    vi.unstubAllGlobals()
    const pending = [{...users[0], password_reset_required: true}, users[1]]
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/admin/users') return jsonResponse({data: pending, stats: {total: 2, members: 2, admins: 0, disabled: 0}})
      if (url === '/admin/tenants') return jsonResponse({data: tenants})
      return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    renderWithConfirm()
    const dialog = await openAssignDrawer(user)
    expect(within(dialog).getByText('等待本人设置新密码')).toBeInTheDocument()
  })

  it('explains a disabled account instead of silently failing', async () => {
    stubWithReset(() => jsonResponse({error: {code: 'account_disabled', message: 'disabled'}}, 403))
    const user = userEvent.setup()
    renderWithConfirm()

    const dialog = await openAssignDrawer(user)
    await user.type(within(dialog).getByPlaceholderText('至少 10 位，需包含两类字符'), 'Interim#2026Reset')
    await user.click(within(dialog).getByRole('button', {name: '重置密码'}))
    const confirmDialog = await screen.findByRole('dialog', {name: /重置 zhangsan 的密码/})
    await user.click(within(confirmDialog).getByRole('button', {name: '重置密码'}))

    expect(await screen.findByText('该账号已停用，请先启用再重置密码。')).toBeInTheDocument()
  })
})

// Opening an account is how the deployment adds people without leaving public
// registration switched on, so the form has to reach the gateway with exactly
// what the operator chose — and nothing it did not.
describe('UsersPage create account', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function stubCreate(onCreate: () => Response = () => jsonResponse({data: {...users[0], id: 'u-new', username: 'wangwu'}}, 201)) {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/admin/users' && (!init?.method || init.method === 'GET')) {
        return jsonResponse({data: users, stats: {total: 2, members: 2, admins: 0, disabled: 0}})
      }
      if (url === '/admin/tenants') return jsonResponse({data: tenants})
      if (url === '/admin/users' && init?.method === 'POST') return onCreate()
      return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  async function openCreateDrawer(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole('button', {name: '新建账号'}))
    return screen.findByRole('dialog', {name: '新建账号'})
  }

  it('sends the chosen username, password, roles and Tenant', async () => {
    const fetchMock = stubCreate()
    const user = userEvent.setup()
    renderUsersPage()

    const dialog = await openCreateDrawer(user)
    await user.type(within(dialog).getByPlaceholderText('用于登录的账号名，例如学号或工号'), 'wangwu')
    await user.type(within(dialog).getByPlaceholderText('至少 10 位，需包含两类字符'), 'Interim#2026New')
    fireEvent.change(within(dialog).getByRole('combobox'), {target: {value: 't-1'}})
    await user.click(within(dialog).getByRole('checkbox', {name: '网关管理员'}))
    await user.click(within(dialog).getByRole('button', {name: '创建账号'}))

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url, init]) => String(url) === '/admin/users' && init?.method === 'POST')
      expect(call).toBeTruthy()
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        username: 'wangwu',
        display_name: '',
        password: 'Interim#2026New',
        roles: ['gateway_admin'],
        tenant_id: 't-1',
      })
    })
  })

  it('will not submit until a username and a password are present', async () => {
    stubCreate()
    const user = userEvent.setup()
    renderUsersPage()

    const dialog = await openCreateDrawer(user)
    const submit = within(dialog).getByRole('button', {name: '创建账号'})
    expect(submit).toBeDisabled()

    await user.type(within(dialog).getByPlaceholderText('用于登录的账号名，例如学号或工号'), 'wangwu')
    expect(submit).toBeDisabled()

    await user.type(within(dialog).getByPlaceholderText('至少 10 位，需包含两类字符'), 'Interim#2026New')
    expect(submit).toBeEnabled()
  })

  it('reports a taken username instead of closing silently', async () => {
    stubCreate(() => jsonResponse({error: {code: 'username_taken', message: 'taken'}}, 409))
    const user = userEvent.setup()
    renderUsersPage()

    const dialog = await openCreateDrawer(user)
    await user.type(within(dialog).getByPlaceholderText('用于登录的账号名，例如学号或工号'), 'zhangsan')
    await user.type(within(dialog).getByPlaceholderText('至少 10 位，需包含两类字符'), 'Interim#2026New')
    await user.click(within(dialog).getByRole('button', {name: '创建账号'}))

    expect(await screen.findByText('该用户名已被使用，请换一个。')).toBeInTheDocument()
  })
})
