import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {UsersPage} from './UsersPage'
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
