import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {RouteEditor} from './RouteEditor'
import {ToastProvider} from '../ui/Toast'

const detail = {
  model: {id: 'm-1', alias: 'demo', display_name: 'Demo', description: '', status: 1, config_version: 1},
  routes: [{id: 'r-1', model_id: 'm-1', model_alias: 'demo', provider_id: 'p-1', provider_name: 'Qwen', upstream_model: 'up-1', priority: 2, weight: 30, timeout_ms: 60000, status: 1}],
  version: 1,
}

function jsonResponse(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
  } as Response
}

function mockFetch() {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/admin/models/m-1/routes' && init?.method === 'PUT') {
      return jsonResponse({error: {code: 'config_version_conflict', message: 'current version is 2'}}, 409)
    }
    if (url === '/admin/models/m-1') return jsonResponse(detail)
    if (url === '/admin/providers') return jsonResponse({data: [{id: 'p-1', name: 'Qwen'}]})
    return jsonResponse({error: {code: 'not_found', message: `unexpected ${url}`}}, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('RouteEditor', () => {
  beforeEach(() => {
    mockFetch()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('keeps the draft and shows the current version on a 409', async () => {
    render(
      <ToastProvider>
        <RouteEditor modelID="m-1" />
      </ToastProvider>,
    )
    const upstream = await screen.findByDisplayValue('up-1')
    fireEvent.change(upstream, {target: {value: 'up-edited'}})
    fireEvent.click(screen.getByRole('button', {name: '保存整组线路'}))

    expect(await screen.findByText(/当前版本为 2/)).toBeInTheDocument()
    expect(screen.getByDisplayValue('up-edited')).toBeInTheDocument()
  })

  it('shows an exact diff and does not write when switching to primary/backup', async () => {
    const fetchMock = mockFetch()
    render(
      <ToastProvider>
        <RouteEditor modelID="m-1" />
      </ToastProvider>,
    )
    await screen.findByDisplayValue('up-1')
    fireEvent.click(screen.getByRole('button', {name: '主备容灾'}))

    expect(await screen.findByText(/会改变以下字段/)).toBeInTheDocument()
    expect(screen.getByText(/优先级 2 → 1/)).toBeInTheDocument()
    expect(screen.getByText(/权重 30 → 100/)).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(false)
  })
})
