import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {AccountSecurity} from './AccountSecurity'
import {ToastProvider} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import type {AdminSessionView, WhoAmI} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const sessionWho: WhoAmI = {user: 'zhangsan', username: 'zhangsan', roles: ['member'], auth_mode: 'session'}

const sessions: AdminSessionView[] = [
  {id: 's-current', current: true, login_method: 'password', ip: '10.0.0.1', user_agent: 'Chrome on Windows', created_at: '2026-10-20T08:00:00Z', last_active_at: '2026-10-22T08:00:00Z', expires_at: '2026-10-23T08:00:00Z'},
  {id: 's-other', current: false, login_method: 'password', ip: '10.0.0.2', user_agent: 'Safari on iPhone', created_at: '2026-10-19T08:00:00Z', last_active_at: '2026-10-21T08:00:00Z', expires_at: '2026-10-22T20:00:00Z'},
]

function stub(opts: {sessions?: 'ok' | 'fail' | 'empty'; password?: {status: number; body: unknown}} = {}) {
  const calls: {url: string; method: string; body?: string}[] = []
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    calls.push({url, method: init?.method || 'GET', body: init?.body as string})
    if (url === '/admin/sessions') {
      if (opts.sessions === 'fail') return jsonResponse({error: {message: '会话服务不可用'}}, 500)
      return jsonResponse({data: opts.sessions === 'empty' ? [] : sessions})
    }
    if (url === '/admin/password' && init?.method === 'POST') {
      const out = opts.password ?? {status: 200, body: {ok: true, message: '密码已更新'}}
      return jsonResponse(out.body, out.status)
    }
    if (url.startsWith('/admin/sessions/') && url.endsWith('/revoke')) return jsonResponse({ok: true})
    return jsonResponse({error: {message: `unexpected ${url}`}}, 404)
  }))
  return calls
}

function renderSecurity(onSignedOut = vi.fn(), props: {minPasswordLength?: number} = {}) {
  render(
    <ToastProvider>
      <ConfirmProvider>
        <AccountSecurity who={sessionWho} onSignedOut={onSignedOut} showIdentity={false} {...props} />
      </ConfirmProvider>
    </ToastProvider>,
  )
  return onSignedOut
}

afterEach(() => vi.unstubAllGlobals())

describe('AccountSecurity sessions', () => {
  // A failed session read is not "no other devices". Claiming there are none
  // would tell an account it is not signed in anywhere else, which is exactly
  // the fact a security page must not invent.
  it('reports a failed session read instead of an empty device list', async () => {
    stub({sessions: 'fail'})
    renderSecurity()

    expect(await screen.findByText(/登录设备读取失败/)).toBeInTheDocument()
    expect(screen.queryByText(/没有其他有效登录会话/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载设备'})).toBeInTheDocument()
  })

  it('says there are no other devices only when the server returned none', async () => {
    stub({sessions: 'empty'})
    renderSecurity()
    expect(await screen.findByText(/没有其他有效登录会话/)).toBeInTheDocument()
  })

  it('marks the current device and names it in the revocation confirmation', async () => {
    stub()
    renderSecurity()

    const row = (await screen.findByText('10.0.0.1')).closest('tr') as HTMLElement
    expect(within(row).getByText('当前设备')).toBeInTheDocument()

    await userEvent.click(within(row).getByRole('button', {name: '撤销'}))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(/当前设备/)
    expect(dialog).toHaveTextContent(/重新登录/)
  })

  it('does not revoke anything when the confirmation is cancelled', async () => {
    const calls = stub()
    renderSecurity()
    await screen.findByText('10.0.0.2')

    await userEvent.click(screen.getAllByRole('button', {name: '撤销'})[1])
    await userEvent.click(await screen.findByRole('button', {name: '取消'}))

    expect(calls.some(c => c.url.endsWith('/revoke'))).toBe(false)
  })

  it('revokes another device and refreshes the list', async () => {
    const calls = stub()
    renderSecurity()
    await screen.findByText('10.0.0.2')

    await userEvent.click(screen.getAllByRole('button', {name: '撤销'})[1])
    await userEvent.click(await screen.findByRole('button', {name: '撤销设备'}))

    await waitFor(() => expect(calls.some(c => c.url === '/admin/sessions/s-other/revoke' && c.method === 'POST')).toBe(true))
    await waitFor(() => expect(calls.filter(c => c.url === '/admin/sessions').length).toBeGreaterThan(1))
  })
})

describe('AccountSecurity password', () => {
  it('uses the deployment password floor in its hint', async () => {
    stub()
    renderSecurity(vi.fn(), {minPasswordLength: 14})
    expect(await screen.findByText(/至少 14 位/)).toBeInTheDocument()
    expect(screen.queryByText(/至少 10 位/)).not.toBeInTheDocument()
  })

  it('rejects a mismatched confirmation without a request and keeps the fields', async () => {
    const calls = stub()
    renderSecurity()

    const fields = await screen.findAllByLabelText(/密码/)
    await userEvent.type(fields[0], 'Old-Password-1!')
    await userEvent.type(fields[1], 'New-Password-2!')
    await userEvent.type(fields[2], 'Different-3!')
    await userEvent.click(screen.getByRole('button', {name: '更新密码'}))

    expect(await screen.findByText('两次输入的新密码不一致')).toBeInTheDocument()
    expect(calls.some(c => c.url === '/admin/password')).toBe(false)
    expect(fields[1]).toHaveValue('New-Password-2!')
  })

  it('keeps the inputs and shows the server reason when the change fails', async () => {
    stub({password: {status: 400, body: {error: {code: 'password_policy_violation', message: 'password is too weak'}}}})
    renderSecurity()

    const fields = await screen.findAllByLabelText(/密码/)
    await userEvent.type(fields[0], 'Old-Password-1!')
    await userEvent.type(fields[1], 'weak')
    await userEvent.type(fields[2], 'weak')
    await userEvent.click(screen.getByRole('button', {name: '更新密码'}))

    expect(await screen.findByText(/password is too weak/)).toBeInTheDocument()
    expect(fields[0]).toHaveValue('Old-Password-1!')
  })

  it('does not send twice while the change is in flight', async () => {
    const calls: string[] = []
    let release!: () => void
    const gate = new Promise<void>(r => { release = r })
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/admin/sessions') return jsonResponse({data: []})
      if (url === '/admin/password') {
        calls.push(url)
        await gate
        return jsonResponse({ok: true, message: '密码已更新'})
      }
      return jsonResponse({error: {message: 'unexpected'}}, 404)
    }))
    renderSecurity()

    const fields = await screen.findAllByLabelText(/密码/)
    await userEvent.type(fields[0], 'Old-Password-1!')
    await userEvent.type(fields[1], 'New-Password-2!')
    await userEvent.type(fields[2], 'New-Password-2!')
    const button = screen.getByRole('button', {name: '更新密码'})
    await userEvent.click(button)
    await userEvent.click(button)

    await waitFor(() => expect(calls.length).toBe(1))
    release()
    await waitFor(() => expect(calls.length).toBe(1))
  })

  it('signs the account out after a successful change', async () => {
    stub()
    const onSignedOut = renderSecurity()

    const fields = await screen.findAllByLabelText(/密码/)
    await userEvent.type(fields[0], 'Old-Password-1!')
    await userEvent.type(fields[1], 'New-Password-2!')
    await userEvent.type(fields[2], 'New-Password-2!')
    await userEvent.click(screen.getByRole('button', {name: '更新密码'}))

    await waitFor(() => expect(onSignedOut).toHaveBeenCalled())
  })
})

describe('AccountSecurity trusted-header mode', () => {
  it('offers no password or device controls it cannot perform', () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    render(
      <ToastProvider>
        <ConfirmProvider>
          <AccountSecurity who={{...sessionWho, auth_mode: 'trusted-header'}} onSignedOut={vi.fn()} showIdentity={false} />
        </ConfirmProvider>
      </ToastProvider>,
    )
    expect(screen.getByText(/统一身份认证平台管理/)).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: '更新密码'})).not.toBeInTheDocument()
    expect(screen.queryByText('登录设备')).not.toBeInTheDocument()
  })
})
