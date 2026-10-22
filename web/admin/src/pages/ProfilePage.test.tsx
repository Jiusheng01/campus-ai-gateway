import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import {ProfilePage} from './ProfilePage'
import type {WhoAmI} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

const member: WhoAmI = {user: 'student1', username: 'student1', roles: ['member'], auth_mode: 'session'}
const admin: WhoAmI = {user: 'zhangsan', username: 'zhangsan', roles: ['super_admin', 'auditor'], auth_mode: 'session'}

function stub(who: WhoAmI | number) {
  vi.stubGlobal('fetch', vi.fn(async () => (
    typeof who === 'number' ? jsonResponse({error: {message: '身份服务不可用'}}, who) : jsonResponse(who)
  )))
}

afterEach(() => vi.unstubAllGlobals())

describe('ProfilePage identity', () => {
  it('shows the roles the server reported, not a status it cannot know', async () => {
    stub(admin)
    render(<ProfilePage who={admin} />)

    expect(await screen.findByText('zhangsan')).toBeInTheDocument()
    expect(screen.getByText('超级管理员')).toBeInTheDocument()
    expect(screen.getByText(/共 2 项/)).toBeInTheDocument()
    // The identity endpoint carries no account-status field; claiming 正常
    // would be a health verdict this page has no data for.
    expect(screen.queryByText('正常')).not.toBeInTheDocument()
    expect(screen.queryByText('账号状态')).not.toBeInTheDocument()
  })

  it('names the sign-in method rather than claiming account health', async () => {
    stub(admin)
    render(<ProfilePage who={admin} />)
    expect(await screen.findByText('登录方式')).toBeInTheDocument()
    expect(screen.getByText('账号密码登录')).toBeInTheDocument()
  })

  it('tells a plain member what they can do without sending them to admin menus', async () => {
    stub(member)
    render(<ProfilePage who={member} />)

    expect(await screen.findByText(/当前账号暂无网关管理权限/)).toBeInTheDocument()
    expect(screen.getByText(/修改本账号密码/)).toBeInTheDocument()
    expect(screen.getByText(/查看和管理自己的登录设备/)).toBeInTheDocument()
  })

  it('reports a failed identity read with a retry, not a blank identity', async () => {
    stub(503)
    render(<ProfilePage who={null} />)

    expect(await screen.findByText('身份服务不可用')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '重新加载'})).toBeInTheDocument()
    expect(screen.queryByText('未知用户')).not.toBeInTheDocument()
  })
})

describe('ProfilePage fallback identity', () => {
  it('still shows the identity passed by the shell while the read is in flight', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => {})))
    render(<ProfilePage who={admin} />)
    expect(screen.getByText('zhangsan')).toBeInTheDocument()
  })

  it('does not title a page it cannot fill with a made-up account', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => {})))
    render(<ProfilePage who={null} />)
    expect(screen.queryByText('未知用户')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toBeInTheDocument()
  })
})
