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
    expect(within(summaryCard('在线')).getByText('3', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('在线')).getByText('(33.33%)')).toBeInTheDocument()
    expect(within(summaryCard('异常 / 停用')).getByText('6', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('异常 / 停用')).getByText('(66.67%)')).toBeInTheDocument()

    await userEvent.type(screen.getByPlaceholderText('请输入渠道名称'), 'alpha')
    await userEvent.click(screen.getByRole('button', {name: '筛选'}))
    await waitFor(() => expect(screen.queryByText('beta')).not.toBeInTheDocument())

    // Filtering narrows the table, not the totals.
    expect(within(summaryCard('渠道总数')).getByText('9', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('在线')).getByText('3', {selector: '.statCardValue'})).toBeInTheDocument()
    expect(within(summaryCard('异常 / 停用')).getByText('6', {selector: '.statCardValue'})).toBeInTheDocument()
  })
})

describe('ProvidersPage channel facts', () => {
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
