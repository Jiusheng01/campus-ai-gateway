import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {render, screen, within} from '@testing-library/react'
import {TenantsPage} from './TenantsPage'
import {ToastProvider} from '../components/ui/Toast'
import type {Tenant} from '../api'

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
      if (url.startsWith('/admin/tenants')) return jsonResponse({data: tenants})
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
    const row = (label: string) => within(card).getByText(label).parentElement as HTMLElement

    expect(row('日 Token')).toHaveTextContent('1,500')
    expect(row('月 Token')).toHaveTextContent('无限制')
    expect(row('月请求')).toHaveTextContent('未设置')
  })
})
