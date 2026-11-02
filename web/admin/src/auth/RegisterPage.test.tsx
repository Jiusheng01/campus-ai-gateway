import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {RegisterPage} from './RegisterPage'
import type {AuthConfig} from '../api'

function jsonResponse(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response
}

const config: AuthConfig = {
  auth_mode: 'session',
  login_enabled: true,
  needs_setup: false,
  setup_local_only: true,
  setup_allowed_here: false,
  min_password_length: 10,
  access_ttl_seconds: 900,
  registration_enabled: true,
}

function renderPage(onDone = vi.fn()) {
  render(
    <RegisterPage
      config={config}
      onDone={onDone}
      onSwitchToLogin={() => {}}
      onAccountCreated={() => {}}
    />,
  )
  return onDone
}

async function fillAndSubmit() {
  const user = userEvent.setup()
  await user.type(screen.getByPlaceholderText('用于登录的账号名'), 'zhangsan')
  await user.type(
    screen.getByPlaceholderText('至少 10 位，需包含字母、数字、符号中的至少两类'),
    'Campus#2026Gate',
  )
  await user.type(screen.getByPlaceholderText('请再次输入密码'), 'Campus#2026Gate')
  await user.click(screen.getByRole('button', {name: '创建账号'}))
  return user
}

describe('public registration errors', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('explains a disabled default Tenant without exposing the server text', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        jsonResponse(
          {error: {code: 'registration_tenant_disabled', message: 'the configured registration tenant is disabled'}},
          409,
        ),
      ),
    )
    renderPage()
    await fillAndSubmit()

    expect(await screen.findByText(/默认租户已停用/)).toBeInTheDocument()
    expect(screen.queryByText(/the configured registration tenant/)).not.toBeInTheDocument()
  })

  it('explains a missing default Tenant', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        jsonResponse(
          {error: {code: 'registration_tenant_not_found', message: 'the configured registration tenant no longer exists'}},
          409,
        ),
      ),
    )
    renderPage()
    await fillAndSubmit()

    expect(await screen.findByText(/默认租户不存在/)).toBeInTheDocument()
  })

  it('lets the user retry after a temporary registration outage', async () => {
    const onDone = vi.fn()
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse({error: {code: 'registration_unavailable', message: 'counter unavailable'}}, 503),
      )
      .mockResolvedValueOnce(
        jsonResponse({
          access_token: 'token',
          expires_at: new Date(Date.now() + 900_000).toISOString(),
          user: {id: 'u-1', username: 'zhangsan', display_name: '', roles: ['member'], password_reset_required: false},
        }),
      )
    vi.stubGlobal('fetch', fetchMock)
    renderPage(onDone)

    const user = await fillAndSubmit()
    expect(await screen.findByText(/注册服务暂时不可用/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', {name: '创建账号'}))
    expect(onDone).toHaveBeenCalledTimes(1)
  })
})
