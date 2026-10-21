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
        if (url.startsWith('/admin/models')) return jsonResponse({data: models})
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
