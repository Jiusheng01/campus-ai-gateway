import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {PublishedModelsPage} from './PublishedModelsPage'
import {ToastProvider} from '../components/ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

// A vision-sounding alias is the case that used to be turned into a 多模态 tag.
// Nothing in the payload backs that claim, so the card must not make it.
const models = [
  {
    id: 'model-vision',
    alias: 'campus-vision',
    display_name: '视觉理解',
    description: '',
    status: 1,
    config_version: 1,
    route_count: 1,
    enabled_route_count: 1,
    statically_routable: true,
    credential_configured: true,
    routing_mode_summary: '',
  },
  {
    id: 'model-chat',
    alias: 'campus-gpt',
    display_name: '通用对话',
    description: '面向全校的通用问答模型。',
    status: 1,
    config_version: 2,
    route_count: 1,
    enabled_route_count: 1,
    statically_routable: true,
    credential_configured: true,
    routing_mode_summary: '单线路',
  },
  {
    id: 'model-embed',
    alias: 'campus-embed',
    display_name: '向量模型',
    description: '文本向量化模型。',
    status: 2,
    config_version: 1,
    route_count: 1,
    enabled_route_count: 0,
    statically_routable: false,
    credential_configured: false,
    routing_mode_summary: '未配置',
  },
]

const providers = [
  {id: 'p-deepseek', name: 'deepseek-official'},
  {id: 'p-qwen', name: 'qwen-official'},
]

const tenants = [
  {id: 't-student', code: 'student', name: '学生', status: 1},
  {id: 't-teacher', code: 'teacher', name: '教职工', status: 1},
  {id: 't-lab', code: 'lab', name: '实验室', status: 2},
]

// The channel filter is backed by the route projection: a Virtual Model is
// associated with a channel through its Routes, never through its alias.
const routes = [
  {
    id: 'r-vision',
    model_id: 'model-vision',
    model_alias: 'campus-vision',
    provider_id: 'p-deepseek',
    provider_name: 'deepseek-official',
    upstream_model: 'deepseek-chat',
    priority: 1,
    weight: 100,
    timeout_ms: 60000,
    status: 1,
  },
  {
    id: 'r-chat',
    model_id: 'model-chat',
    model_alias: 'campus-gpt',
    provider_id: 'p-qwen',
    provider_name: 'qwen-official',
    upstream_model: 'qwen-plus',
    priority: 1,
    weight: 100,
    timeout_ms: 60000,
    status: 1,
  },
  {
    id: 'r-embed',
    model_id: 'model-embed',
    model_alias: 'campus-embed',
    provider_id: 'p-deepseek',
    provider_name: 'deepseek-official',
    upstream_model: 'deepseek-embed',
    priority: 1,
    weight: 100,
    timeout_ms: 60000,
    status: 2,
  },
]

function renderPage(props: {canAuthorize?: boolean} = {}) {
  return render(
    <ToastProvider>
      <PublishedModelsPage canAuthorize={props.canAuthorize} />
    </ToastProvider>,
  )
}

describe('PublishedModelsPage', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/admin/models') return jsonResponse({data: models})
        if (url.endsWith('/impact')) {
          return jsonResponse({
            alias: 'campus-vision',
            enabled_routes: 1,
            enabled_providers: 1,
            explicit_keys: 2,
            wildcard_keys: 1,
          })
        }
        if (url.includes('/authorizations')) return jsonResponse({alias: 'campus-vision', data: []})
        if (url.includes('/runtime-summary')) {
          return jsonResponse({
            alias: 'campus-vision',
            from: '2026-01-01T00:00:00Z',
            to: '2026-01-02T00:00:00Z',
            totals: {requests: 0, errors: 0, success_rate: 0, p95_latency_ms: 0, failover: 0, tokens: 0},
            recent: [],
          })
        }
        if (url.startsWith('/admin/models/')) {
          const id = url.slice('/admin/models/'.length)
          const model = models.find(m => m.id === id) ?? models[0]
          return jsonResponse({model, routes: routes.filter(r => r.model_id === id), version: model.config_version})
        }
        if (url.startsWith('/admin/model-test-options')) {
          return jsonResponse({data: models.map(m => ({id: m.id, alias: m.alias, status: m.status}))})
        }
        if (url.startsWith('/admin/providers')) return jsonResponse({data: providers})
        if (url.startsWith('/admin/routes')) return jsonResponse({data: routes})
        if (url.startsWith('/admin/tenants')) return jsonResponse({data: tenants})
        return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
      }),
    )
  })
  afterEach(() => vi.unstubAllGlobals())

  it('does not infer a capability tag from text in the alias', async () => {
    renderPage()
    const card = (await screen.findByText('campus-vision')).closest('article')
    expect(card).not.toBeNull()

    expect(card).not.toHaveTextContent('多模态')
    expect(card).not.toHaveTextContent('图像理解')
    expect(card).not.toHaveTextContent('视觉分析')

    // Tags that do render are read from model fields, not from the alias.
    expect(card).toHaveTextContent('凭据已配置')
  })

  it('renders neutral placeholders when the description and routing summary are empty', async () => {
    renderPage()
    const card = (await screen.findByText('campus-vision')).closest('article')
    expect(card).not.toBeNull()
    const scope = within(card as HTMLElement)

    expect(scope.getByText('—')).toBeInTheDocument()
    expect(scope.getByText('未配置')).toBeInTheDocument()
    expect(card).not.toHaveTextContent('校园通用大语言模型')
    expect(card).not.toHaveTextContent('智能路由')
  })

  it('narrows the rendered cards when a channel is selected and restores them when cleared', async () => {
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByText('campus-gpt')).toBeInTheDocument()
    expect(screen.getAllByRole('article')).toHaveLength(3)

    await user.selectOptions(screen.getByLabelText(/上游渠道/), 'qwen-official')

    await waitFor(() => expect(screen.getAllByRole('article')).toHaveLength(1))
    expect(screen.getByText('campus-gpt')).toBeInTheDocument()
    expect(screen.queryByText('campus-vision')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText(/上游渠道/), '')

    await waitFor(() => expect(screen.getAllByRole('article')).toHaveLength(3))
    expect(screen.getByText('campus-vision')).toBeInTheDocument()
  })

  it('counts the model and tenant collections it loaded', async () => {
    renderPage({canAuthorize: true})
    await screen.findByText('campus-gpt')

    const value = (label: string) =>
      screen.getByText(label, {selector: '.statCardLabel'}).closest('.statCard')?.querySelector('.statCardValue')?.textContent

    // Three models, two of them enabled; two tenants enabled out of three.
    expect(value('已发布模型')).toBe('3')
    expect(value('启用中')).toBe('2')
    expect(value('启用租户')).toBe('2')

    const tenantCalls = vi.mocked(fetch).mock.calls.filter(([url]) => String(url).startsWith('/admin/tenants'))
    expect(tenantCalls).toHaveLength(1)
  })

  it('does not request the tenant list for a role that may not read it', async () => {
    renderPage()
    await screen.findByText('campus-gpt')

    expect(screen.queryByText('启用租户', {selector: '.statCardLabel'})).toBeNull()
    const tenantCalls = vi.mocked(fetch).mock.calls.filter(([url]) => String(url).startsWith('/admin/tenants'))
    expect(tenantCalls).toHaveLength(0)
  })
})

/**
 * These two workflows predate the redesign and were dropped from the card
 * layout. They are operator-facing capabilities, not decoration: a diagnostic
 * the dashboard can start must also be startable from the model it concerns,
 * and a model must not be disabled before its impact is shown.
 */
describe('PublishedModelsPage restored workflows', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/admin/models') return jsonResponse({data: models})
        if (url.endsWith('/impact')) {
          return jsonResponse({
            alias: 'campus-vision',
            enabled_routes: 1,
            enabled_providers: 1,
            explicit_keys: 2,
            wildcard_keys: 1,
          })
        }
        if (url.includes('/authorizations')) return jsonResponse({alias: 'campus-vision', data: []})
        if (url.includes('/runtime-summary')) {
          return jsonResponse({
            alias: 'campus-vision',
            from: '2026-01-01T00:00:00Z',
            to: '2026-01-02T00:00:00Z',
            totals: {requests: 0, errors: 0, success_rate: 0, p95_latency_ms: 0, failover: 0, tokens: 0},
            recent: [],
          })
        }
        if (url.startsWith('/admin/models/')) {
          const id = url.slice('/admin/models/'.length)
          const model = models.find(m => m.id === id) ?? models[0]
          return jsonResponse({model, routes: routes.filter(r => r.model_id === id), version: model.config_version})
        }
        if (url.startsWith('/admin/model-test-options')) {
          return jsonResponse({data: models.map(m => ({id: m.id, alias: m.alias, status: m.status}))})
        }
        if (url.startsWith('/admin/providers')) return jsonResponse({data: providers})
        if (url.startsWith('/admin/routes')) return jsonResponse({data: routes})
        if (url.startsWith('/admin/tenants')) return jsonResponse({data: tenants})
        return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
      }),
    )
  })
  afterEach(() => vi.unstubAllGlobals())

  it('offers a next step for the model returned by a successful publication', async () => {
    const user = userEvent.setup()
    const original = vi.mocked(fetch).getMockImplementation()!
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      if (String(input) === '/admin/providers') return jsonResponse({data: [{...providers[0], status: 1}]})
      if (String(input) === `/admin/providers/${providers[0].id}/models`) return jsonResponse({data: [{id: 'pm-1', upstream_model: 'deepseek-chat', status: 1}]})
      if (String(input) === '/admin/models/publish') return jsonResponse({model: models[0], created: true, route_created: true, version: 1}, 201)
      return original(input, init)
    })
    renderPage()
    await user.click(await screen.findByRole('button', {name: '发布模型'}))
    await waitFor(() => expect(screen.getByLabelText(/对外名称/)).toHaveValue('deepseek-chat'))
    await user.click(screen.getByRole('button', {name: '发布'}))
    await user.click(await screen.findByRole('button', {name: '查看发布结果'}))
    expect(await screen.findByText('模型详情：campus-vision')).toBeInTheDocument()
  })

  it('runs a model diagnostic from the model detail drawer', async () => {
    const user = userEvent.setup()
    renderPage()
    const card = (await screen.findByText('campus-vision')).closest('article')
    expect(card).not.toBeNull()

    await user.click(within(card as HTMLElement).getByRole('button', {name: /详情/}))
    expect(await screen.findByText('模型详情：campus-vision')).toBeInTheDocument()

    await user.click(await screen.findByRole('button', {name: /运行模型诊断/}))

    expect(await screen.findByText('模型连通测试')).toBeInTheDocument()
    // The drawer opens on the model whose detail was being read.
    expect(screen.getByLabelText(/对外模型/)).toHaveValue('campus-vision')
  })

  it('previews the disabling impact before it disables a model', async () => {
    const user = userEvent.setup()
    renderPage()
    const card = (await screen.findByText('campus-vision')).closest('article')
    expect(card).not.toBeNull()

    await user.click(within(card as HTMLElement).getByRole('button', {name: '停用'}))

    // The preview describes what will change; it must appear before the PUT.
    expect(await screen.findByText('停用模型：campus-vision')).toBeInTheDocument()
    expect(screen.getByText(/启用线路：1 条/)).toBeInTheDocument()
    expect(screen.getByText(/显式允许该 alias 的 Key：2 个/)).toBeInTheDocument()
    expect(
      vi.mocked(fetch).mock.calls.some(([, init]) => init?.method === 'PUT'),
    ).toBe(false)

    await user.click(screen.getByRole('button', {name: '确认停用'}))

    await waitFor(() => {
      const put = vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === 'PUT')
      expect(put).toBeTruthy()
      expect(String(put?.[0])).toBe('/admin/models/model-vision')
      expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({status: 2, expected_version: 1})
    })
  })
})
