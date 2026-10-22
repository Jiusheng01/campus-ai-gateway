import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {RoutesPage} from './RoutesPage'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {RouteView} from '../api'

function route(over: Partial<RouteView> = {}): RouteView {
  return {
    id: 'r-1',
    model_id: 'm-1',
    model_alias: 'campus-chat',
    provider_id: 'p-1',
    provider_name: 'qwen',
    upstream_model: 'qwen-plus',
    priority: 1,
    weight: 100,
    timeout_ms: 60000,
    status: 1,
    model_display_name: '校园问答',
    config_version: 3,
    provider_status: 1,
    credential_configured: true,
    ...over,
  }
}

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function stubFetch(rows: RouteView[]) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url.startsWith('/admin/routes')) return jsonResponse({data: rows})
    if (url.startsWith('/admin/models/')) {
      return jsonResponse({
        model: {id: 'm-1', alias: 'campus-chat', display_name: '校园问答', description: '', status: 1, config_version: 3},
        routes: rows,
        version: 3,
      })
    }
    if (url.startsWith('/admin/providers')) return jsonResponse({data: [{id: 'p-1', name: 'qwen'}, {id: 'p-2', name: 'deepseek'}]})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderPage() {
  return render(
    <ToastProvider>
      <ConfirmProvider>
        <RoutesPage />
      </ConfirmProvider>
    </ToastProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('RoutesPage status semantics', () => {
  // The data plane still picks a line whose channel is disabled? No — it does
  // not: a disabled channel contributes no line. Saying "已启用" for a model
  // whose every line points at a disabled channel, or whose lines are all
  // disabled themselves, would report service that cannot happen.
  it('does not report a model as enabled when no line can carry traffic', async () => {
    stubFetch([
      route({id: 'r-1', status: 2, provider_status: 1}),
      route({id: 'r-2', status: 1, provider_status: 2, upstream_model: 'qwen-turbo'}),
    ])

    renderPage()
    const card = (await screen.findByText('campus-chat')).closest('article') as HTMLElement
    expect(within(card).getByText('无可用线路')).toBeInTheDocument()
    expect(within(card).queryByText('已启用')).not.toBeInTheDocument()
  })

  it('reports a model as enabled when at least one line is enabled on an enabled channel', async () => {
    stubFetch([
      route({id: 'r-1', status: 2, provider_status: 1}),
      route({id: 'r-2', status: 1, provider_status: 1, upstream_model: 'qwen-turbo'}),
    ])

    renderPage()
    const card = (await screen.findByText('campus-chat')).closest('article') as HTMLElement
    expect(within(card).getByText('已启用')).toBeInTheDocument()
  })
})

describe('RoutesPage credential facts', () => {
  // Credential configured is not a health check: the line may still be failing.
  it('does not describe a configured credential as healthy', async () => {
    stubFetch([route({credential_configured: true})])

    renderPage()
    await screen.findByText('campus-chat')
    expect(screen.queryByText(/✓ 正常/)).not.toBeInTheDocument()
    expect(screen.getByText('已配置')).toBeInTheDocument()
  })

  it('marks a missing credential and a disabled channel as separate facts', async () => {
    stubFetch([route({credential_configured: false, provider_status: 2})])

    renderPage()
    await screen.findByText('campus-chat')
    expect(screen.getByText('未配置凭据')).toBeInTheDocument()
    expect(screen.getByText('渠道停用')).toBeInTheDocument()
  })
})

describe('RoutesPage line context', () => {
  it('links each line to its channel and the model heading to its detail', async () => {
    stubFetch([route()])

    renderPage()
    const card = (await screen.findByText('campus-chat')).closest('article') as HTMLElement
    // The channel column links by id: channel names are not unique, and the
    // channel page narrows its own list from the id.
    expect(within(card).getByRole('link', {name: 'qwen'})).toHaveAttribute('href', '/admin-ui/providers?provider=p-1')
    expect(within(card).getByRole('link', {name: 'campus-chat'})).toHaveAttribute('href', '/admin-ui/models?model=m-1')
  })

  // A filter that is present in the address is what a link back from a channel
  // page can point at, and what survives a reload.
  it('reads its filters from the address and writes them back', async () => {
    const fetchMock = stubFetch([route()])
    window.history.replaceState({}, '', window.location.origin + '/admin-ui/routes?model=campus-chat')
    renderPage()

    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([url]) => String(url).includes('model=campus-chat'))).toBe(true),
    )
    expect(screen.getByDisplayValue('campus-chat')).toBeInTheDocument()

    await userEvent.clear(screen.getByPlaceholderText('请输入渠道名称'))
    await userEvent.type(screen.getByPlaceholderText('请输入渠道名称'), 'qwen')
    await userEvent.click(screen.getByRole('button', {name: '筛选'}))

    await waitFor(() => expect(window.location.search).toContain('provider=qwen'))
    expect(window.location.search).toContain('model=campus-chat')
    window.history.replaceState({}, '', window.location.origin + '/admin-ui/routes')
  })
})

describe('RoutesPage summary', () => {
  // The four cards classify the configuration. Calling them strategies implies
  // a choice the router does not offer: what differs is how priority and weight
  // are set per model, and every model's line set is one of these shapes.
  it('counts models by the shape of their line set', async () => {
    stubFetch([
      route({id: 'r-1', model_id: 'm-1', model_alias: 'one', priority: 1}),
      route({id: 'r-2', model_id: 'm-2', model_alias: 'two', priority: 1, provider_id: 'p-1'}),
      route({id: 'r-3', model_id: 'm-2', model_alias: 'two', priority: 1, provider_id: 'p-2', provider_name: 'deepseek'}),
    ])

    renderPage()
    await screen.findByText('one')
    const summary = screen.getByLabelText('线路概况')
    expect(within(summary).getByText('单线路')).toBeInTheDocument()
    expect(within(summary).getByText('同层分流')).toBeInTheDocument()
    expect(within(summary).queryByText(/策略/)).not.toBeInTheDocument()
  })
})

describe('RoutesPage editing', () => {
  it('opens the editor for the model whose line was clicked', async () => {
    stubFetch([route()])
    renderPage()
    const card = (await screen.findByText('campus-chat')).closest('article') as HTMLElement
    await userEvent.click(within(card).getByRole('button', {name: '编辑线路'}))

    expect(await screen.findByRole('dialog', {name: /编辑线路：campus-chat/})).toBeInTheDocument()
  })
})
