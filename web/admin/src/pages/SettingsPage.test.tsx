import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {SettingsPage} from './SettingsPage'
import type {AuthConfig, WhoAmI} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body), json: async () => body} as Response
}

const sessionConfig: AuthConfig = {
  auth_mode: 'session',
  login_enabled: true,
  needs_setup: false,
  setup_local_only: false,
  setup_allowed_here: true,
  min_password_length: 10,
  access_ttl_seconds: 900,
  registration_enabled: false,
}

const who: WhoAmI = {user: 'zhangsan', username: 'zhangsan', roles: ['super_admin'], auth_mode: 'session'}

afterEach(() => vi.unstubAllGlobals())

describe('SettingsPage deployment facts', () => {
  it('renders the real session facts from the auth config', () => {
    render(<SettingsPage who={who} authConfig={sessionConfig} />)

    expect(screen.getByText('账号密码登录')).toBeInTheDocument()
    expect(screen.getByText('已启用')).toBeInTheDocument() // login
    expect(screen.getByText('未开放')).toBeInTheDocument() // registration
    expect(screen.getByText('10 位')).toBeInTheDocument()
    expect(screen.getByText('15 分钟')).toBeInTheDocument()
  })

  it('states that every value is read-only and where it comes from', () => {
    render(<SettingsPage who={who} authConfig={sessionConfig} />)
    expect(screen.getAllByText(/只读/).length).toBeGreaterThan(0)
    expect(screen.getByText(/部署环境变量|启动配置/)).toBeInTheDocument()
    // A read-only page must not offer a save it cannot perform.
    expect(screen.queryByRole('button', {name: /保存/})).not.toBeInTheDocument()
  })

  it('marks an unrecognised auth mode as unknown rather than healthy', () => {
    render(<SettingsPage who={who} authConfig={{...sessionConfig, auth_mode: 'ldap-legacy'}} />)
    expect(screen.getByText('ldap-legacy')).toBeInTheDocument()
    expect(screen.getAllByText(/未知模式|未在本控制台|未识别的认证方式/).length).toBeGreaterThan(0)
    expect(screen.queryByText('账号密码登录')).not.toBeInTheDocument()
  })

  it('does not claim a default deployment when no config was read', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({error: {message: '配置服务不可用'}}, 500))
    vi.stubGlobal('fetch', fetchMock)
    render(<SettingsPage who={null} authConfig={null} />)

    expect((await screen.findAllByText(/配置服务不可用|无法读取认证配置/)).length).toBeGreaterThan(0)
    expect(screen.queryByText('账号密码登录')).not.toBeInTheDocument()
    expect(screen.queryByText('已启用')).not.toBeInTheDocument()
  })

  it('retries the config read when it failed', async () => {
    let attempts = 0
    vi.stubGlobal('fetch', vi.fn(async () => {
      attempts += 1
      return attempts === 1
        ? jsonResponse({error: {message: '配置服务不可用'}}, 500)
        : jsonResponse(sessionConfig)
    }))
    render(<SettingsPage who={null} authConfig={null} />)

    const retry = await screen.findByRole('button', {name: '重新加载'})
    await userEvent.click(retry)

    expect(await screen.findByText('账号密码登录')).toBeInTheDocument()
    expect(screen.getByText('已启用')).toBeInTheDocument()
  })
})

describe('SettingsPage grouping', () => {
  // The ticket asks for groups by responsibility instead of one long block of
  // prose; login and identity are separate questions from session lifetime.
  it('groups identity, sign-in policy and session lifetime', () => {
    render(<SettingsPage who={who} authConfig={sessionConfig} />)
    expect(screen.getByRole('heading', {name: '认证方式'})).toBeInTheDocument()
    expect(screen.getByRole('heading', {name: '登录与注册'})).toBeInTheDocument()
    expect(screen.getByRole('heading', {name: '会话与密码'})).toBeInTheDocument()
  })

  it('names the acting account and its role scope', () => {
    render(<SettingsPage who={who} authConfig={sessionConfig} />)
    const scope = screen.getByRole('region', {name: '当前操作者'})
    expect(within(scope).getByText('zhangsan')).toBeInTheDocument()
    expect(within(scope).getByText(/超级管理员/)).toBeInTheDocument()
  })
})
