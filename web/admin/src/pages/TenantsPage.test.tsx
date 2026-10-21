import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {render, screen, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {TenantsPage} from './TenantsPage'
import {ToastProvider} from '../components/ui/Toast'
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
        <TenantsPage />
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
        <TenantsPage />
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
        <TenantsPage />
      </ToastProvider>,
    )

    const list = await screen.findByRole('region', {name: '租户卡片列表'})
    const card = within(list).getAllByRole('article')[0]
    await user.click(within(card).getByRole('button', {name: /查看用量/}))

    expect(await screen.findByRole('dialog', {name: '租户用量'})).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith('/admin/tenants/t-1/usage', expect.anything())
  })
})
