import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {TenantsPage} from './TenantsPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {Tenant} from '../api'

/**
 * The table/card switch is CSS-driven: `.desktopOnly` / `.mobileOnly` in
 * styles.css flip at the sheet's 900px breakpoint. jsdom does not apply the
 * stylesheet, so both branches are in the DOM in every test below; these tests
 * assert what the card branch carries, not which branch a viewport would show.
 * Visibility itself is checked in the running console.
 */

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

// Three states of one quota field, side by side: a configured number, a zero
// that means unlimited, and a field the response omits entirely.
const tenants = [
  {
    id: 't-1',
    code: 'cs-dept',
    name: '计算机学院',
    status: 1,
    daily_token_limit: 1500,
    monthly_token_limit: 0,
    // monthly_request_limit intentionally missing
    monthly_budget_micros: 0,
    rpm_limit: 600,
    tpm_limit: 100000,
    max_concurrency: 50,
    allowed_models: ['*'],
  },
] as Tenant[]

describe('TenantsPage on a narrow screen', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/admin/tenants') return jsonResponse({data: tenants})
      if (url === '/admin/tenants/t-1/usage') {
        return jsonResponse({
          tenant_id: 't-1',
          daily_requests: 12,
          monthly_requests: 240,
          daily_tokens: 800,
          monthly_tokens: 12000,
        })
      }
      return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('renders a card per tenant with its identity, quota dimensions and actions', async () => {
    render(
      <ToastProvider>
        <TenantsPage tenants={tenants} onReload={() => {}} />
      </ToastProvider>,
    )

    // The card list is the mobile representation kept alongside the table.
    const list = await screen.findByRole('region', {name: '租户卡片列表'})
    const cards = within(list).getAllByRole('article')
    expect(cards).toHaveLength(1)

    const card = cards[0]
    expect(within(card).getByText('计算机学院')).toBeInTheDocument()
    expect(within(card).getByText('cs-dept')).toBeInTheDocument()
    expect(within(card).getByText('启用')).toBeInTheDocument()
    for (const label of ['日 Token', '月 Token', '月请求', 'RPM', 'TPM', '并发']) {
      expect(within(card).getByText(label)).toBeInTheDocument()
    }
    expect(within(card).getByRole('button', {name: /查看用量/})).toBeInTheDocument()
    expect(within(card).getByRole('button', {name: /编辑/})).toBeInTheDocument()
  })

  it('renders a card limit as its number, zero as 无限制 and an absent field as 未设置', async () => {
    render(
      <ToastProvider>
        <TenantsPage tenants={tenants} onReload={() => {}} />
      </ToastProvider>,
    )

    const list = await screen.findByRole('region', {name: '租户卡片列表'})
    const card = within(list).getAllByRole('article')[0]
    const cardRow = (label: string) => within(card).getByText(label).parentElement as HTMLElement

    expect(cardRow('日 Token')).toHaveTextContent('1,500')
    expect(cardRow('月 Token')).toHaveTextContent('无限制')
    expect(cardRow('月请求')).toHaveTextContent('未设置')

    // The row keeps the same three states: both branches share one renderer.
    const table = screen.getByRole('table')
    const tableRow = within(table).getByText('计算机学院').closest('tr') as HTMLElement
    expect(tableRow).toHaveTextContent('1,500')
    expect(tableRow).toHaveTextContent('无限制')
    expect(tableRow).toHaveTextContent('未设置')
  })

  it('opens the usage drawer from a card, like the row action does', async () => {
    const user = userEvent.setup()
    render(
      <ToastProvider>
        <TenantsPage tenants={tenants} onReload={() => {}} />
      </ToastProvider>,
    )

    const list = await screen.findByRole('region', {name: '租户卡片列表'})
    const card = within(list).getAllByRole('article')[0]
    await user.click(within(card).getByRole('button', {name: /查看用量/}))

    expect(await screen.findByRole('dialog', {name: '租户用量'})).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/admin/tenants/t-1/usage', expect.anything())
  })

  it('deletes a tenant only after its name is typed, and names the keys that go with it', async () => {
    const calls: {url: string; method: string}[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        calls.push({url, method: init?.method || 'GET'})
        if (url === '/admin/tenants') return jsonResponse({data: tenants})
        if (url === '/admin/tenants/t-1' && init?.method === 'DELETE') {
          return jsonResponse({ok: true, id: 't-1', deleted_keys: 2})
        }
        return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
      }),
    )
    const user = userEvent.setup()
    render(
      <ToastProvider>
        <ConfirmProvider>
          <TenantsPage tenants={tenants} keyCounts={{'t-1': 2}} onReload={() => {}} />
        </ConfirmProvider>
      </ToastProvider>,
    )

    // The table row and the phone card both carry the action; either opens the
    // same confirmation.
    const buttons = await screen.findAllByRole('button', {name: /^删除$/})
    await user.click(buttons[0])

    expect(screen.getByText(/2 个 API Key/)).toBeInTheDocument()
    // The keys go; the people do not. member Accounts survive with their
    // Tenant membership cleared, which is a different consequence and has to
    // be stated before the operator types the name.
    expect(screen.getByText(/成员账号不会被删除/)).toBeInTheDocument()
    expect(screen.getByText(/变为「未分配」状态/)).toBeInTheDocument()
    expect(calls.some(c => c.method === 'DELETE')).toBe(false)

    // set the whole value in one change event: userEvent types character by
    // character, and each keystroke re-renders the confirmation dialog.
    fireEvent.change(screen.getByLabelText(/输入租户名称/), {target: {value: '计算机学院'}})
    await user.click(screen.getByRole('button', {name: '确认删除'}))

    await waitFor(() =>
      expect(calls.some(c => c.method === 'DELETE' && c.url === '/admin/tenants/t-1')).toBe(true),
    )
  })
})
