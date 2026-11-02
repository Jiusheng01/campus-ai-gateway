import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {TenantsPage} from './TenantsPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {ModelConfig, Tenant} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const tenant: Tenant = {
  id: 't-1',
  code: 'cs-dept',
  name: '计算机学院',
  status: 1,
  parent_id: null,
  path: '/t-1/',
  depth: 0,
  kind: 'dept',
  daily_token_limit: 1500,
  monthly_token_limit: null,
  monthly_request_limit: 200,
  monthly_budget_micros: 0,
  rpm_limit: 600,
  tpm_limit: 100000,
  max_concurrency: 50,
  allowed_models: ['campus-chat'],
}

const model: ModelConfig = {
  id: 'm-1',
  alias: 'campus-chat',
  display_name: '校园问答',
  description: '',
  status: 1,
  config_version: 1,
}

/** The page loads tenants, models and keys itself; every case answers those reads. */
function stubData(post?: (url: string) => Response) {
  const calls: {url: string; method: string; body?: string}[] = []
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method || 'GET'
    calls.push({url, method, body: init?.body as string})
    if (method === 'GET' && url === '/admin/tenants') return jsonResponse({data: [tenant]})
    if (method === 'GET' && url === '/admin/models') return jsonResponse({data: [model]})
    if (method === 'GET' && url === '/admin/api-keys') return jsonResponse({data: []})
    if (method === 'POST' && url === '/admin/tenants') {
      return post ? post(url) : jsonResponse({id: 't-2'}, 201)
    }
    return jsonResponse({error: {message: `unexpected ${method} ${url}`}}, 404)
  }))
  return calls
}

function renderPage() {
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <TenantsPage />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('TenantsPage lifecycle', () => {
  it('creates a tenant through the real endpoint and closes only on success', async () => {
    const calls = stubData()
    renderPage()

    await userEvent.click((await screen.findAllByRole('button', {name: /新增租户/}))[0])
    const dialog = await screen.findByRole('dialog', {name: '新增租户'})
    await userEvent.type(within(dialog).getByLabelText(/租户编码/), 'new-dept')
    await userEvent.type(within(dialog).getByLabelText(/租户名称/), '外国语学院')
    await userEvent.click(within(dialog).getByRole('button', {name: '创建租户'}))

    await waitFor(() => expect(calls.some(c => c.method === 'POST' && c.url === '/admin/tenants')).toBe(true))
    const post = calls.find(c => c.method === 'POST')!
    expect(JSON.parse(String(post.body))).toMatchObject({code: 'new-dept', name: '外国语学院', status: 1})
    await waitFor(() => expect(screen.queryByRole('dialog', {name: '新增租户'})).not.toBeInTheDocument())
    // The page reloads its own data after a successful create; the second GET
    // is the visible proof that the saved row is what the operator sees next.
    expect(calls.filter(c => c.method === 'GET' && c.url === '/admin/tenants').length).toBeGreaterThan(1)
  })

  it('keeps the form open when the server refuses the tenant', async () => {
    stubData(() => jsonResponse({error: {code: 'invalid_tenant', message: '租户编码已被使用'}}, 400))
    renderPage()

    await userEvent.click((await screen.findAllByRole('button', {name: /新增租户/}))[0])
    const dialog = await screen.findByRole('dialog', {name: '新增租户'})
    await userEvent.type(within(dialog).getByLabelText(/租户编码/), 'cs-dept')
    await userEvent.type(within(dialog).getByLabelText(/租户名称/), '计算机学院')
    await userEvent.click(within(dialog).getByRole('button', {name: '创建租户'}))

    expect(await screen.findByText('租户编码已被使用')).toBeInTheDocument()
    expect(screen.getByDisplayValue('cs-dept')).toBeInTheDocument()
    expect(screen.getByRole('dialog', {name: '新增租户'})).toBeInTheDocument()
  })

  // The allowlist is the ceiling for personal API Keys, not a data-plane
  // switch: existing keys keep the scope they were issued with. The form says
  // so where the operator narrows it.
  it('says the model list limits key issuance and does not revoke existing keys', async () => {
    stubData()
    renderPage()

    await userEvent.click((await screen.findAllByRole('button', {name: /编辑/}))[0])
    const dialog = await screen.findByRole('dialog', {name: '编辑租户'})
    expect(within(dialog).getByText(/个人 API Key/)).toBeInTheDocument()
    expect(within(dialog).getByText(/不会.*撤销|不.*追溯/)).toBeInTheDocument()
  })
})
