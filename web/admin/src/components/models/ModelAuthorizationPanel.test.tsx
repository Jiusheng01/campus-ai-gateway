import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {ModelAuthorizationPanel} from './ModelAuthorizationPanel'
import {ToastProvider} from '../ui/Toast'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const keys = [
  {api_key_id: 'key-explicit', name: 'explicit', tenant_id: 't', tenant_name: 'Tenant', status: 1, wildcard: false, allowed: false},
  {api_key_id: 'key-wildcard', name: 'wildcard', tenant_id: 't', tenant_name: 'Tenant', status: 1, wildcard: true, allowed: true},
]

describe('ModelAuthorizationPanel', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/admin/models/m-1/authorizations' && (!init || !init.method)) {
        return jsonResponse({alias: 'demo', data: keys})
      }
      if (url === '/admin/models/m-1/authorizations' && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as {api_key_ids: string[]; allow: boolean}
        const results = body.api_key_ids.map(id => ({
          api_key_id: id,
          outcome: body.allow ? 'added' : (id === 'key-wildcard' ? 'wildcard_refused' : 'removed'),
        }))
        return jsonResponse({alias: 'demo', results})
      }
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('merges a model into one key and reports wildcard refusal per key', async () => {
    render(
      <ToastProvider>
        <ModelAuthorizationPanel modelID="m-1" canAuthorize />
      </ToastProvider>,
    )
    await screen.findByText('explicit')
    fireEvent.click(screen.getByLabelText('选择 Key explicit'))
    fireEvent.click(screen.getByRole('button', {name: '授权所选 Key'}))
    expect(await screen.findByText(/已添加/)).toBeInTheDocument()

    fireEvent.click(screen.getByLabelText('选择 Key wildcard'))
    fireEvent.click(screen.getByRole('button', {name: '撤销所选 Key'}))
    expect(await screen.findByText(/通配符 Key，未修改/)).toBeInTheDocument()
  })
})
