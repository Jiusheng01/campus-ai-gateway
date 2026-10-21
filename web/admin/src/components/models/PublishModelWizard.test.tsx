import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {PublishModelWizard} from './PublishModelWizard'
import {ToastProvider} from '../ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function mockFetch(onPublish?: (body: Record<string, unknown>) => Response) {
  // The shared api() helper accepts a RequestInfo; in these tests it is always
  // a string path.
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === '/admin/providers') {
      return jsonResponse({data: [{id: 'p-1', name: 'qwen', status: 1}, {id: 'p-2', name: 'doubao', status: 1}]})
    }
    if (url === '/admin/providers/p-1/models') {
      return jsonResponse({data: [
        {id: 'pm-1', provider_id: 'p-1', upstream_model: 'qwen-plus', source: 'manual', status: 1},
        {id: 'pm-2', provider_id: 'p-1', upstream_model: 'qwen-off', source: 'manual', status: 2},
      ]})
    }
    if (url === '/admin/models/publish' && init?.method === 'POST') {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>
      if (onPublish) return onPublish(body)
      return jsonResponse({model: {id: 'm-1', alias: body.alias}, created: true, route_created: true, version: 1}, 201)
    }
    return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderWizard(onPublished = vi.fn(), onClose = vi.fn()) {
  render(
    <ToastProvider>
      <PublishModelWizard onClose={onClose} onPublished={onPublished} />
    </ToastProvider>,
  )
  return onPublished
}

describe('PublishModelWizard', () => {
  beforeEach(() => mockFetch())
  afterEach(() => vi.unstubAllGlobals())

  it('lists only enabled candidates and publishes the selected one', async () => {
    const onPublished = renderWizard()
    const candidate = await screen.findByRole('combobox', {name: /候选模型/})
    await waitFor(() => expect(candidate).toHaveTextContent('qwen-plus'))
    expect(candidate).not.toHaveTextContent('qwen-off')
    expect(screen.getByLabelText(/对外名称/)).toHaveValue('qwen-plus')

    fireEvent.click(screen.getByText('显示名称与描述（可选）'))
    fireEvent.change(screen.getByLabelText(/显示名称/), {target: {value: '智能助手模型'}})
    fireEvent.click(screen.getByRole('button', {name: '发布'}))

    const fetchMock = vi.mocked(fetch)
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url, init]) => url === '/admin/models/publish' && init?.method === 'POST')
      expect(call).toBeTruthy()
      const body = JSON.parse(String(call![1]!.body))
      expect(body).toMatchObject({
        provider_model_id: 'pm-1',
        alias: 'qwen-plus',
        display_name: '智能助手模型',
        append_to_existing: false,
      })
    })
    await waitFor(() => expect(onPublished).toHaveBeenCalledWith(expect.objectContaining({model: {id: 'm-1', alias: 'qwen-plus'}, created: true})))
  })

  it('requires an explicit append after an alias conflict', async () => {
    let calls = 0
    vi.unstubAllGlobals()
    mockFetch(body => {
      calls++
      if (calls === 1) return jsonResponse({error: {code: 'alias_exists', message: 'exists'}}, 409)
      return jsonResponse({model: {id: 'm-1', alias: body.alias}, created: false, route_created: true, version: 2})
    })
    renderWizard()
    const candidate = await screen.findByRole('combobox', {name: /候选模型/})
    await waitFor(() => expect(candidate).toHaveTextContent('qwen-plus'))

    fireEvent.click(screen.getByRole('button', {name: '发布'}))
    expect(await screen.findByText(/该对外名称已存在/)).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText(/确认把此候选追加为备用线路/))
    fireEvent.click(screen.getByRole('button', {name: '确认发布'}))

    const fetchMock = vi.mocked(fetch)
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([url, init]) => url === '/admin/models/publish' && init?.method === 'POST')
      expect(posts).toHaveLength(2)
      expect(JSON.parse(String(posts[1][1]!.body)).append_to_existing).toBe(true)
    })
  })

  it('keeps the current channel selection when an older candidate request finishes last', async () => {
    let finishFirst: (response: Response) => void = () => {}
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/admin/providers?') || url === '/admin/providers') {
        return jsonResponse({data: [{id: 'p-1', name: 'qwen', status: 1}, {id: 'p-2', name: 'doubao', status: 1}]})
      }
      if (url === '/admin/providers/p-1/models') return new Promise<Response>(resolve => { finishFirst = resolve })
      return jsonResponse({data: [{id: 'pm-current', upstream_model: 'doubao-pro', status: 1}]})
    }))
    renderWizard()
    await waitFor(() => expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).endsWith('/p-1/models'))).toBe(true))
    fireEvent.change(screen.getByLabelText(/上游渠道/), {target: {value: 'p-2'}})
    await waitFor(() => expect(screen.getByLabelText(/候选模型/)).toHaveTextContent('doubao-pro'))
    finishFirst(jsonResponse({data: [{id: 'pm-old', upstream_model: 'qwen-old', status: 1}]}))
    await waitFor(() => expect(screen.getByLabelText(/候选模型/)).not.toHaveTextContent('qwen-old'))
    expect(screen.getByLabelText(/对外名称/)).toHaveValue('doubao-pro')
  })

  it('offers every enabled channel from the complete provider collection', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === '/admin/providers') return jsonResponse({data: [
        {id: 'disabled', name: '已停用渠道', status: 2},
        ...Array.from({length: 205}, (_, index) => ({id: `p-${index}`, name: `渠道-${index}`, status: 1})),
      ]})
      return jsonResponse({data: []})
    }))
    renderWizard()
    expect(await screen.findByRole('option', {name: '渠道-204'})).toBeInTheDocument()
    expect(screen.queryByRole('option', {name: '已停用渠道'})).not.toBeInTheDocument()
  })

  it('lets the administrator retry loading candidates without losing the selected channel', async () => {
    const original = vi.mocked(fetch).getMockImplementation()!
    let failed = false
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      if (String(input) === '/admin/providers/p-1/models' && !failed) {
        failed = true
        return jsonResponse({error: {message: '候选模型暂时不可读'}}, 503)
      }
      return original(input, init)
    })
    renderWizard()
    expect(await screen.findByRole('alert')).toHaveTextContent('候选模型暂时不可读')
    fireEvent.click(screen.getByRole('button', {name: '重试加载候选模型'}))
    await waitFor(() => expect(screen.getByLabelText(/候选模型/)).toHaveTextContent('qwen-plus'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows an inline alias error and does not publish an empty alias', async () => {
    renderWizard()
    await waitFor(() => expect(screen.getByLabelText(/对外名称/)).toHaveValue('qwen-plus'))
    fireEvent.change(screen.getByLabelText(/对外名称/), {target: {value: ' '}})
    fireEvent.click(screen.getByRole('button', {name: '发布'}))
    expect(screen.getByRole('textbox', {name: /对外名称/})).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('alert')).toHaveTextContent('对外名称（alias）不能为空')
    expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url) === '/admin/models/publish')).toBe(false)
  })

  it('cancels without publishing', async () => {
    const onClose = vi.fn()
    renderWizard(vi.fn(), onClose)
    await waitFor(() => expect(screen.getByLabelText(/对外名称/)).toHaveValue('qwen-plus'))
    fireEvent.click(screen.getByRole('button', {name: '取消'}))
    expect(onClose).toHaveBeenCalledOnce()
    expect(vi.mocked(fetch).mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
  })

  it('explains unavailable channels and links to the actual channel configuration page', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    renderWizard()
    expect(await screen.findByRole('link', {name: '前往上游渠道配置'})).toHaveAttribute('href', '/admin-ui/providers')
    expect(screen.getByRole('button', {name: '发布'})).toBeDisabled()
  })

  it('preserves the draft and reports a server refusal without publishing success', async () => {
    mockFetch(() => jsonResponse({error: {message: '发布被服务端拒绝'}}, 500))
    const published = renderWizard()
    await waitFor(() => expect(screen.getByLabelText(/对外名称/)).toHaveValue('qwen-plus'))
    fireEvent.change(screen.getByLabelText(/对外名称/), {target: {value: 'campus-custom'}})
    fireEvent.click(screen.getByRole('button', {name: '发布'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('发布被服务端拒绝')
    expect(screen.getByLabelText(/对外名称/)).toHaveValue('campus-custom')
    expect(published).not.toHaveBeenCalled()
  })
})
