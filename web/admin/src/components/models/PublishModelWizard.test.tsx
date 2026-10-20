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
    if (url.startsWith('/admin/providers?status=1')) {
      return jsonResponse({data: [{id: 'p-1', name: 'qwen'}, {id: 'p-2', name: 'doubao'}]})
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

function renderWizard(onPublished = vi.fn()) {
  render(
    <ToastProvider>
      <PublishModelWizard onClose={() => {}} onPublished={onPublished} />
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
    await waitFor(() => expect(onPublished).toHaveBeenCalled())
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
})
