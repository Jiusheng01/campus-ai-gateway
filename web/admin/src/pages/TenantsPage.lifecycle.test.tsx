import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {TenantsPage} from './TenantsPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {Tenant} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const tenant: Tenant = {
  id: 't-1',
  code: 'cs-dept',
  name: '计算机学院',
  status: 1,
  daily_token_limit: 1500,
  monthly_token_limit: 0,
  monthly_request_limit: 200,
  monthly_budget_micros: 0,
  rpm_limit: 600,
  tpm_limit: 100000,
  max_concurrency: 50,
  allowed_models: ['campus-chat'],
}

function renderPage(opts: {tenants?: Tenant[]; models?: {id: string; alias: string; display_name: string; description: string; status: number; config_version: number}[]; onReload?: () => void} = {}) {
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <TenantsPage
          tenants={opts.tenants ?? [tenant]}
          models={opts.models ?? [{id: 'm-1', alias: 'campus-chat', display_name: '校园问答', description: '', status: 1, config_version: 1}]}
          onReload={opts.onReload ?? vi.fn()}
        />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('TenantsPage lifecycle', () => {
  it('creates a tenant through the real endpoint and closes only on success', async () => {
    const calls: {url: string; method: string; body?: string}[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({url, method: init?.method || 'GET', body: init?.body as string})
      if (url === '/admin/tenants' && init?.method === 'POST') return jsonResponse({id: 't-2'}, 201)
      return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
    }))
    const onReload = vi.fn()
    renderPage({onReload})

    await userEvent.click(screen.getAllByRole('button', {name: /新增租户/})[0])
    const dialog = await screen.findByRole('dialog', {name: '新增租户'})
    await userEvent.type(within(dialog).getByLabelText(/租户编码/), 'new-dept')
    await userEvent.type(within(dialog).getByLabelText(/租户名称/), '外国语学院')
    await userEvent.click(within(dialog).getByRole('button', {name: '创建租户'}))

    await waitFor(() => expect(calls.some(c => c.method === 'POST' && c.url === '/admin/tenants')).toBe(true))
    const post = calls.find(c => c.method === 'POST')!
    expect(JSON.parse(String(post.body))).toMatchObject({code: 'new-dept', name: '外国语学院', status: 1})
    await waitFor(() => expect(onReload).toHaveBeenCalled())
  })

  it('keeps the form open when the server refuses the tenant', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({error: {code: 'invalid_tenant', message: '租户编码已被使用'}}, 400)))
    renderPage()

    await userEvent.click(screen.getAllByRole('button', {name: /新增租户/})[0])
    const dialog = await screen.findByRole('dialog', {name: '新增租户'})
    await userEvent.type(within(dialog).getByLabelText(/租户编码/), 'cs-dept')
    await userEvent.click(within(dialog).getByRole('button', {name: '创建租户'}))

    expect(await screen.findByText('租户编码已被使用')).toBeInTheDocument()
    expect(screen.getByDisplayValue('cs-dept')).toBeInTheDocument()
  })

  // The allowlist is the ceiling for personal API Keys, not a data-plane
  // switch: existing keys keep the scope they were issued with. The form says
  // so where the operator narrows it.
  it('says the model list limits key issuance and does not revoke existing keys', async () => {
    renderPage()

    await userEvent.click(screen.getAllByRole('button', {name: /编辑/})[0])
    const dialog = await screen.findByRole('dialog', {name: '编辑租户'})
    expect(within(dialog).getByText(/个人 API Key/)).toBeInTheDocument()
    expect(within(dialog).getByText(/不会.*撤销|不.*追溯/)).toBeInTheDocument()
  })
})
