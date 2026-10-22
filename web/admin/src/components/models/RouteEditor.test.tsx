import {afterEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {RouteEditor} from './RouteEditor'
import {ToastProvider} from '../ui/Toast'
import {ConfirmProvider} from '../ui/ConfirmDialog'
import type {ModelDetail} from '../../api'

function route(over: Partial<ModelDetail['routes'][number]> = {}): ModelDetail['routes'][number] {
  return {
    id: 'r-1',
    model_id: 'm-1',
    model_alias: 'demo',
    provider_id: 'p-1',
    provider_name: 'Qwen',
    upstream_model: 'up-1',
    priority: 2,
    weight: 30,
    timeout_ms: 60000,
    status: 1,
    model_display_name: 'Demo',
    config_version: 1,
    provider_status: 1,
    credential_configured: true,
    ...over,
  }
}

function detail(routes = [route()], version = 1): ModelDetail {
  return {
    model: {id: 'm-1', alias: 'demo', display_name: 'Demo', description: '', status: 1, config_version: version},
    routes,
    version,
  }
}

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

type SaveResult = {status: number; body: unknown}
function stubFetch(save?: (body: {expected_version: number; routes: unknown[]}) => SaveResult) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/admin/models/m-1/routes' && init?.method === 'PUT') {
      const parsed = JSON.parse(String(init.body)) as {expected_version: number; routes: unknown[]}
      const result = save ? save(parsed) : {status: 200, body: {version: 2, routes: detail().routes}}
      return jsonResponse(result.body, result.status)
    }
    if (url === '/admin/models/m-1') return jsonResponse(detail())
    if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}, {id: 'p-2', name: 'DeepSeek'}]})
    return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const writes = (fetchMock: ReturnType<typeof stubFetch>) =>
  fetchMock.mock.calls.filter(([, init]) => init?.method === 'PUT')

function renderEditor(onSaved = vi.fn()) {
  render(
    <ToastProvider>
      <ConfirmProvider>
        <RouteEditor modelID="m-1" onSaved={onSaved} />
      </ConfirmProvider>
    </ToastProvider>,
  )
  return onSaved
}

afterEach(() => vi.unstubAllGlobals())

describe('RouteEditor save preview', () => {
  // The mode switch already previewed its own changes. This is the final check
  // before a write: every edit the operator made has to appear in one list, and
  // the request must not leave the page until they confirm it.
  it('shows the full difference and writes nothing until it is confirmed', async () => {
    const fetchMock = stubFetch()
    const onSaved = renderEditor()
    const upstream = await screen.findByDisplayValue('up-1')
    fireEvent.change(upstream, {target: {value: 'up-edited'}})

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))

    const dialog = await screen.findByRole('dialog', {name: /确认保存线路/})
    expect(within(dialog).getByText(/改为：.*up-edited/)).toBeInTheDocument()
    expect(writes(fetchMock)).toHaveLength(0)

    await userEvent.click(within(dialog).getByRole('button', {name: '确认保存'}))

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1))
    const sent = JSON.parse(String(writes(fetchMock)[0][1]!.body))
    expect(sent.expected_version).toBe(1)
    expect(sent.routes[0].upstream_model).toBe('up-edited')
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
  })

  it('keeps the draft and sends nothing when the preview is cancelled', async () => {
    const fetchMock = stubFetch()
    renderEditor()
    const upstream = await screen.findByDisplayValue('up-1')
    fireEvent.change(upstream, {target: {value: 'up-edited'}})

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))
    await userEvent.click(await screen.findByRole('button', {name: '返回修改'}))

    expect(writes(fetchMock)).toHaveLength(0)
    expect(screen.getByDisplayValue('up-edited')).toBeInTheDocument()
  })

  it('does not ask for confirmation when the draft is unchanged', async () => {
    const fetchMock = stubFetch()
    renderEditor()
    await screen.findByDisplayValue('up-1')

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1))
    expect(screen.queryByRole('dialog', {name: /确认保存线路/})).not.toBeInTheDocument()
  })

  it('lists a disabled line and a removed line in the same preview', async () => {
    stubFetch()
    renderEditor()
    await screen.findByDisplayValue('up-1')

    fireEvent.change(screen.getByLabelText('状态'), {target: {value: '2'}})
    await userEvent.click(screen.getByRole('button', {name: '添加线路'}))

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))
    const dialog = await screen.findByRole('dialog', {name: /确认保存线路/})
    expect(within(dialog).getByText(/线路 r-1：状态 启用 → 停用/)).toBeInTheDocument()
    expect(within(dialog).getByText(/新增：p-1 \//)).toBeInTheDocument()
  })
})

describe('RouteEditor version conflicts', () => {
  it('keeps the draft and reloads only when asked, for a 409 that carries the version', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/admin/models/m-1/routes' && init?.method === 'PUT') {
        return jsonResponse({error: {code: 'config_version_conflict', message: 'The route set changed since it was loaded; current version is 4'}}, 409)
      }
      if (url === '/admin/models/m-1') return jsonResponse(detail())
      if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}]})
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
    const onSaved = renderEditor()
    fireEvent.change(await screen.findByDisplayValue('up-1'), {target: {value: 'up-edited'}})

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))
    await userEvent.click(await screen.findByRole('button', {name: '确认保存'}))

    expect(await screen.findByText(/当前版本为 4/)).toBeInTheDocument()
    // The draft survives the conflict; reloading is the operator's decision.
    expect(screen.getByDisplayValue('up-edited')).toBeInTheDocument()
    expect(onSaved).not.toHaveBeenCalled()
  })

  // The server reports the version in the body today, but the console must not
  // depend on a parseable message to notice a conflict: a 409 with nothing to
  // read is still a conflict, and silently showing it as a save failure would
  // invite the operator to retry over another administrator's edit.
  it('shows a conflict without a version when the 409 carries no parseable one', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/admin/models/m-1/routes' && init?.method === 'PUT') {
        return jsonResponse({error: {code: 'config_version_conflict', message: 'conflict'}}, 409)
      }
      if (url === '/admin/models/m-1') return jsonResponse(detail())
      if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}]})
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
    renderEditor()
    fireEvent.change(await screen.findByDisplayValue('up-1'), {target: {value: 'up-edited'}})

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))
    await userEvent.click(await screen.findByRole('button', {name: '确认保存'}))

    expect(await screen.findByText(/线路已被其他管理员修改/)).toBeInTheDocument()
    expect(screen.getByDisplayValue('up-edited')).toBeInTheDocument()
  })

  it('re-reads the latest version and replaces the draft only after the operator asks', async () => {
    let version = 1
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/admin/models/m-1/routes' && init?.method === 'PUT') {
        return jsonResponse({error: {message: 'The route set changed since it was loaded; current version is 9'}}, 409)
      }
      if (url === '/admin/models/m-1') {
        return jsonResponse(detail([route({upstream_model: version === 1 ? 'up-1' : 'server-version'})], version))
      }
      if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}]})
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
    renderEditor()
    fireEvent.change(await screen.findByDisplayValue('up-1'), {target: {value: 'up-edited'}})

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))
    await userEvent.click(await screen.findByRole('button', {name: '确认保存'}))
    await screen.findByText(/当前版本为 9/)

    version = 9
    await userEvent.click(screen.getByRole('button', {name: '放弃草稿并加载最新版本'}))

    expect(await screen.findByDisplayValue('server-version')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('up-edited')).not.toBeInTheDocument()
  })

  it('reports a non-conflict failure without losing the draft', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/admin/models/m-1/routes' && init?.method === 'PUT') {
        return jsonResponse({error: {code: 'invalid_route_set', message: 'an enabled line that is new or changed must have weight greater than 0'}}, 400)
      }
      if (url === '/admin/models/m-1') return jsonResponse(detail())
      if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}]})
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
    renderEditor()
    fireEvent.change(await screen.findByDisplayValue('up-1'), {target: {value: 'up-edited'}})

    await userEvent.click(screen.getByRole('button', {name: '保存整组线路'}))
    await userEvent.click(await screen.findByRole('button', {name: '确认保存'}))

    expect(await screen.findByText(/must have weight greater than 0/)).toBeInTheDocument()
    expect(screen.getByDisplayValue('up-edited')).toBeInTheDocument()
  })
})

describe('RouteEditor mode preview', () => {
  it('shows an exact diff and does not write when switching to primary/backup', async () => {
    const fetchMock = stubFetch()
    renderEditor()
    await screen.findByDisplayValue('up-1')
    fireEvent.click(screen.getByRole('button', {name: '主备容灾'}))

    expect(await screen.findByText(/会改变以下字段/)).toBeInTheDocument()
    expect(screen.getByText(/优先级 2 → 1/)).toBeInTheDocument()
    expect(screen.getByText(/权重 30 → 100/)).toBeInTheDocument()
    expect(writes(fetchMock)).toHaveLength(0)
  })

  // A disabled line takes no traffic, so it is not part of the target share.
  it('excludes a disabled line from the weighted shares', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url === '/admin/models/m-1') {
        return jsonResponse(detail([
          route({id: 'r-1', upstream_model: 'a', priority: 1, weight: 3, status: 1}),
          route({id: 'r-2', upstream_model: 'b', priority: 1, weight: 9, status: 2}),
          route({id: 'r-3', upstream_model: 'c', priority: 1, weight: 1, status: 1}),
        ]))
      }
      if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}]})
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
    renderEditor()
    await screen.findByDisplayValue('a')
    fireEvent.click(screen.getByRole('button', {name: '加权分流'}))

    const shares = await screen.findByText(/目标流量比例/)
    expect(within(shares.parentElement as HTMLElement).getByText('75.0%')).toBeInTheDocument()
    expect(within(shares.parentElement as HTMLElement).getByText('25.0%')).toBeInTheDocument()
    expect(within(shares.parentElement as HTMLElement).getByText(/1 条停用线路不参与分配/)).toBeInTheDocument()
  })
})
