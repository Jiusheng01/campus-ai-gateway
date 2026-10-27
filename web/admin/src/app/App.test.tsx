import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import App from './App'
import {clearSession} from '../api'

// The login helper parses with r.json(), while the shared api() helper parses
// with r.text(); the stub has to satisfy both shapes.
function jsonResponse(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response
}


// The sign-in response carries the fence flag, so a reset account must land on
// the password screen without a reload. Before this was threaded through, the
// account reached the console and saw a 403 on every page.
describe('App routes a fenced account to the password screen', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    clearSession()
  })

  it('shows the reset screen straight after signing in', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/admin/auth-config') {
        return jsonResponse({
          auth_mode: 'session',
          login_enabled: true,
          needs_setup: false,
          setup_local_only: true,
          setup_allowed_here: false,
          min_password_length: 10,
          access_ttl_seconds: 900,
          registration_enabled: true,
        })
      }
      if (url === '/admin/auth/refresh') return jsonResponse({error: {code: 'unauthenticated'}}, 401)
      if (url === '/admin/login') {
        return jsonResponse({
          access_token: 'token',
          expires_at: new Date(Date.now() + 900_000).toISOString(),
          user: {
            id: 'u-1',
            username: 'zhangsan',
            display_name: '张三',
            roles: ['member'],
            password_reset_required: true,
          },
        })
      }
      return jsonResponse({error: {code: 'not_found'}}, 404)
    })
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(<App />)

    const username = await screen.findByPlaceholderText('请输入用户名')
    await user.type(username, 'zhangsan')
    await user.type(screen.getByPlaceholderText('请输入密码'), 'Interim#2026Reset')
    // The submit button's accessible name depends on whether the tab strip is
    // rendered, so it is found by class rather than by label text.
    await user.click(document.querySelector('.authSubmit') as HTMLElement)

    expect(await screen.findByRole('heading', {name: '设置你的新密码'})).toBeInTheDocument()
  })
})
