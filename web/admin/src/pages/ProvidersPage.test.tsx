import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {ProvidersPage} from './ProvidersPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {Provider} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function provider(over: Partial<Provider> = {}): Provider {
  return {
    id: 'p-alpha',
    name: 'alpha',
    base_url: 'https://alpha.example/v1',
    protocol: 'openai-chat',
    provider_type: 'openai',
    description: '',
    credential_env: 'ALPHA_API_KEY',
    credential: {source_type: 'env', configured: true, last4: ''},
    status: 1,
    candidate_model_count: 3,
    referenced_route_count: 2,
    referenced_model_count: 1,
    ...over,
  }
}

const status = {
  gateway: true,
  postgres: true,
  redis: true,
  data_plane: 'direct',
  stats: {today_requests: 0, today_tokens: 0, today_errors: 0, avg_latency_ms: 0, p95_latency_ms: 0, failover_count: 0},
  provider_stats: [],
  // The whole configuration: nine channels, three of them enabled. This is the
  // fact the summary cards describe, whatever the table is filtered to.
  config_counts: {models: 0, enabled_models: 0, providers: 9, enabled_providers: 3},
  route_cache: {built_at: null, stale: false, generation: 0},
  circuits: [],
}

function stubProviders(routes: (url: string) => Response, statusBody: Record<string, unknown> = status) {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.startsWith('/admin/status')) return jsonResponse(statusBody)
    if (url.startsWith('/admin/providers')) return routes(url)
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  }))
}

function renderPage() {
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <ProvidersPage />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

function summaryCard(label: string): HTMLElement {
  const summary = screen.getByLabelText('渠道概览统计')
  return within(summary).getByText(label).closest('.statCard') as HTMLElement
}

function row(name: string): HTMLElement {
  const cell = screen.getByText(name).closest('tr')
  if (!cell) throw new Error(`row for ${name} not found`)
  return cell as HTMLElement
}

afterEach(() => vi.unstubAllGlobals())

describe('ProvidersPage channel summary', () => {
  it('counts the whole channel list and keeps those totals when the table is filtered', async () => {
    const alpha = provider()
    const beta = provider({id: 'p-beta', name: 'beta', status: 2})
    stubProviders(url => {
      // The list endpoint is narrowed by the same filters the table sends.
      if (url.includes('name=alpha')) return jsonResponse({data: [alpha], total: 1})
      return jsonResponse({data: [alpha, beta], total: 9})
    })

    renderPage()
    expect(await screen.findByText('alpha')).toBeInTheDocument()

    const totalCard = await waitFor(() => summaryCard('渠道总数'))
    expect(within(totalCard).getByText('9', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('已启用')).getByText('3', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('已启用')).getByText('(33.33%)')).toBeInTheDocument()
    expect(within(summaryCard('已停用')).getByText('6', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('已停用')).getByText('(66.67%)')).toBeInTheDocument()

    await userEvent.type(screen.getByPlaceholderText('请输入渠道名称'), 'alpha')
    await userEvent.click(screen.getByRole('button', {name: '筛选'}))
    await waitFor(() => expect(screen.queryByText('beta')).not.toBeInTheDocument())

    // Filtering narrows the table, not the totals.
    expect(within(summaryCard('渠道总数')).getByText('9', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('已启用')).getByText('3', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('已停用')).getByText('6', {selector: '.statCardValue'})).toBeInTheDocument()
  })
})

describe('ProvidersPage channel facts', () => {
  it('previews the cascade and requires the channel name, leaving the channel present when deletion fails', async () => {
    const mutations: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/admin/status') return jsonResponse(status)
      if (url.endsWith('/impact')) return jsonResponse({routes: 2, provider_models: 3, orphaned_models: 1, affected_models: [{model_id: 'm1', alias: 'qa-public-model', routes_to_provider: 2, remaining_routes: 0, orphaned: true}]})
      if (init?.method === 'DELETE') {mutations.push(url); return jsonResponse({error: {message: '删除操作失败，请重试'}}, 500)}
      return jsonResponse({data: [provider()], total: 1})
    }))
    renderPage()
    await screen.findByText('alpha')
    await userEvent.click(within(row('alpha')).getByRole('button', {name: '删除'}))
    expect(await screen.findByText(/失去全部线路/)).toBeInTheDocument()
    expect(screen.getByText(/qa-public-model/)).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '永久删除'})).toBeDisabled()
    await userEvent.click(screen.getByRole('button', {name: '取消'}))
    expect(mutations).toEqual([])
    await userEvent.click(within(row('alpha')).getByRole('button', {name: '删除'}))
    await userEvent.type(await screen.findByLabelText(/输入渠道名/), 'alpha')
    expect(screen.getByRole('button', {name: '永久删除'})).toBeEnabled()
    await userEvent.click(screen.getByRole('button', {name: '永久删除'}))
    await waitFor(() => expect(mutations).toHaveLength(1))
    expect(await screen.findByRole('alert')).toHaveTextContent('删除操作失败')
    expect(row('alpha')).toBeInTheDocument()
    expect(mutations).toHaveLength(1)
  })

  it('opens a read-only detail view and updates its diagnostic facts after the real response', async () => {
    let current = provider()
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === '/admin/status') return jsonResponse(status)
      if (init?.method === 'POST') {
        current = {...current, last_test: {at: '2026-10-22T01:20:00Z', ok: true, status_code: 200, latency_ms: 120, upstream_model: 'qa-model'}}
        return jsonResponse({channel: 'alpha', upstream_model: 'qa-model', status_code: 200, latency_ms: 120, input_tokens: 2, output_tokens: 1, total_tokens: 3})
      }
      return jsonResponse({data: [current], total: 1})
    }))
    renderPage()
    await screen.findByText('alpha')
    await userEvent.click(within(row('alpha')).getByRole('button', {name: '详情'}))
    const drawer = screen.getByRole('dialog')
    expect(within(drawer).queryByRole('button', {name: '保存修改'})).not.toBeInTheDocument()
    expect(within(drawer).getByText('尚未诊断')).toBeInTheDocument()
    await userEvent.click(within(drawer).getByRole('button', {name: '连通诊断'}))
    await userEvent.type(screen.getByLabelText(/上游模型/), 'qa-model')
    await userEvent.click(screen.getByRole('button', {name: '开始诊断'}))
    expect(await within(drawer).findByText(/成功 · HTTP 200/)).toBeInTheDocument()
  })

  it('keeps an edit draft after a save failure and refreshes the actual saved channel after retry', async () => {
    let current = provider()
    let rejectSave = true
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/admin/status') return jsonResponse(status)
      if (init?.method === 'PUT') {
        if (rejectSave) return jsonResponse({error: {message: '连接地址不允许访问'}}, 400)
        current = {...current, ...JSON.parse(String(init.body))}
        return jsonResponse(current)
      }
      return jsonResponse({data: [current], total: 1})
    }))
    renderPage()
    await screen.findByText('alpha')
    await userEvent.click(within(row('alpha')).getByRole('button', {name: '编辑'}))
    const name = screen.getByLabelText(/渠道名称/)
    await userEvent.clear(name)
    await userEvent.type(name, 'alpha-updated')
    await userEvent.click(screen.getByRole('button', {name: '保存修改'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('连接地址不允许访问')
    expect(name).toHaveValue('alpha-updated')
    rejectSave = false
    await userEvent.click(screen.getByRole('button', {name: '保存修改'}))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(await screen.findByText('alpha-updated')).toBeInTheDocument()
  })

  it('uses the stored Vendor classification and never describes enabled channels as online', async () => {
    stubProviders(() => jsonResponse({data: [provider({name: 'qwen', provider_type: 'deepseek'}), provider({id: 'p-other', name: 'openai', provider_type: ''})], total: 2}))
    renderPage()
    const first = await waitFor(() => row('qwen'))
    expect(within(first).getByText('DeepSeek')).toBeInTheDocument()
    expect(within(first).queryByText(/通义千问/)).not.toBeInTheDocument()
    expect(within(first).getByText('已启用')).toBeInTheDocument()
    expect(within(first).queryByText('在线')).not.toBeInTheDocument()
    expect(within(row('openai')).getByText('未分类')).toBeInTheDocument()
  })

  it('starts a new server-side search from the first page when Enter is pressed', async () => {
    const urls: string[] = []
    stubProviders(url => {
      urls.push(url)
      return jsonResponse({data: [provider()], total: 30})
    })
    renderPage()
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', {name: '下一页'}))
    await waitFor(() => expect(urls.at(-1)).toContain('offset=15'))
    await userEvent.type(screen.getByPlaceholderText('请输入渠道名称'), 'alpha{Enter}')
    await waitFor(() => expect(urls.at(-1)).toContain('name=alpha'))
    expect(urls.at(-1)).toContain('offset=0')
  })

  it('renders an open Provider Circuit as a label distinct from the disabled state', async () => {
    const alpha = provider()
    const beta = provider({id: 'p-beta', name: 'beta', status: 2})
    stubProviders(
      () => jsonResponse({data: [alpha, beta], total: 2}),
      {
        ...status,
        circuits: [
          {provider: 'alpha', state: 'open', failures: 7, retry_in_ms: 12000},
          {provider: 'beta', state: 'closed', failures: 0, retry_in_ms: 0},
        ],
      },
    )
    vi.spyOn(Date.prototype, 'toLocaleTimeString').mockReturnValue('09:41:00')

    renderPage()
    const openRow = await waitFor(() => row('alpha'))
    expect(within(openRow).getByText('open')).toBeInTheDocument()
    expect(within(openRow).getByText(/本实例/)).toBeInTheDocument()
    expect(within(openRow).getByText('12 秒后探测')).toBeInTheDocument()
    // An open circuit is not the channel's enabled state: it must not be
    // rendered as "已停用".
    expect(within(openRow).queryByText('已停用')).not.toBeInTheDocument()

    const disabledRow = row('beta')
    expect(within(disabledRow).getByText('已停用')).toBeInTheDocument()
    expect(within(disabledRow).getByText('closed')).toBeInTheDocument()
  })

  it('renders referenced Route and Virtual Model counts only when the payload carries them', async () => {
    const referenced = provider({referenced_route_count: 4, referenced_model_count: 2})
    const absent = provider({
      id: 'p-beta',
      name: 'beta',
      referenced_route_count: undefined,
      referenced_model_count: undefined,
    })
    stubProviders(() => jsonResponse({data: [referenced, absent], total: 2}))

    renderPage()
    const referencedRow = await waitFor(() => row('alpha'))
    expect(within(referencedRow).getByText('线路 4')).toBeInTheDocument()
    expect(within(referencedRow).getByText('对外模型 2')).toBeInTheDocument()

    // Omitted counts are unknown, not zero: the response never said "0".
    const absentRow = row('beta')
    expect(within(absentRow).queryByText('线路 0')).not.toBeInTheDocument()
    expect(within(absentRow).queryByText('对外模型 0')).not.toBeInTheDocument()
    expect(within(absentRow).queryByText(/线路 \d/)).not.toBeInTheDocument()
    expect(within(absentRow).queryByText(/对外模型 \d/)).not.toBeInTheDocument()
  })

  it('shows the circuit state and dependency counts in the channel drawer', async () => {
    const alpha = provider({referenced_route_count: 4, referenced_model_count: 2})
    stubProviders(
      () => jsonResponse({data: [alpha], total: 1}),
      {
        ...status,
        circuits: [{provider: 'alpha', state: 'open', failures: 7, retry_in_ms: 12000}],
      },
    )

    renderPage()
    const alphaRow = await waitFor(() => row('alpha'))
    await userEvent.click(within(alphaRow).getByRole('button', {name: '详情'}))

    const drawer = await screen.findByRole('dialog')
    expect(within(drawer).getByText('渠道事实')).toBeInTheDocument()
    expect(within(drawer).getByText('引用线路')).toBeInTheDocument()
    expect(within(drawer).getByText('4 条')).toBeInTheDocument()
    expect(within(drawer).getByText('关联对外模型')).toBeInTheDocument()
    expect(within(drawer).getByText('2 个')).toBeInTheDocument()
    // The drawer names the instance scope and its failures, not "healthy".
    expect(within(drawer).getByText('open')).toBeInTheDocument()
    expect(within(drawer).getByText('7 次连续失败', {exact: false})).toBeInTheDocument()
  })

  it('renders the credential warning as the warn status chip', async () => {
    const alpha = provider({credential: {source_type: 'env', configured: false, last4: ''}})
    stubProviders(() => jsonResponse({data: [alpha], total: 1}))

    renderPage()
    const tag = await screen.findByText('环境变量未就绪')
    expect(tag).toHaveClass('statusChip', 'warn')
  })
})
