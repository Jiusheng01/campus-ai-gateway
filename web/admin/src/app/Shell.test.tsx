import {afterEach, describe, expect, it, vi} from 'vitest'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {Shell} from './Shell'

function jsonResponse(body: unknown, status = 200) {
  return {ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body)} as Response
}

function renderShell(roles: string[], workspace: 'admin' | 'console' = 'admin') {
  return render(
    <Shell
      page={workspace === 'console' ? 'overview' : 'dashboard'}
      workspace={workspace}
      onNavigate={() => {}}
      collapsed={false}
      onToggleCollapse={() => {}}
      dark={false}
      onToggleTheme={() => {}}
      who={{user: 'ops', username: 'ops', roles, auth_mode: 'session'}}
      sessionMode
      refreshing={false}
      onSignOut={() => {}}
    >
      <div>page content</div>
    </Shell>,
  )
}

/**
 * The notification bell is the only part of the shell that shows gateway data.
 * It used to open a static list of two invented messages, one of them stamped
 * "刚刚" — a claim about the deployment that nothing had observed.
 */
describe('Shell notification surface', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('shows an honest empty state when the gateway has recorded no security events', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    renderShell(['auditor'])

    const bell = await screen.findByRole('button', {name: '系统通知'})
    await userEvent.click(bell)

    expect(await screen.findByText('暂无安全事件')).toBeInTheDocument()
    expect(screen.queryByText('刚刚')).not.toBeInTheDocument()
    expect(screen.queryByText('校园 AI 模型网关控制台已就绪')).not.toBeInTheDocument()
  })

  it('renders the security events the gateway returned, with their own age', async () => {
    const createdAt = new Date(Date.now() - 3 * 24 * 60 * 60 * 1000).toISOString()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        jsonResponse({
          data: [
            {
              id: 1,
              request_id: 'req-1',
              tenant_id: 't-1',
              api_key_id: 'k-1',
              event_type: 'invalid_api_key',
              client_ip: '10.0.0.7',
              requested_model: 'campus-gpt',
              detail: '凭据已失效',
              created_at: createdAt,
            },
          ],
        }),
      ),
    )
    renderShell(['auditor'])

    const bell = await screen.findByRole('button', {name: '系统通知'})
    await waitFor(() => expect(bell.querySelector('.badgeDot')).not.toBeNull())
    await userEvent.click(bell)

    expect(await screen.findByText('无效 API Key')).toBeInTheDocument()
    expect(screen.getByText(/凭据已失效/)).toBeInTheDocument()
    expect(screen.getByText(/10\.0\.0\.7/)).toBeInTheDocument()
    expect(screen.getByText('3 天前')).toBeInTheDocument()
    // Opening the panel clears the attention dot: nothing else tells the
    // operator which events they have already looked at.
    expect(bell.querySelector('.badgeDot')).toBeNull()
  })

  it('offers the bell only to roles the endpoint would accept', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({data: []}))
    vi.stubGlobal('fetch', fetchMock)
    renderShell(['member'])

    expect(await screen.findByText('page content')).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: '系统通知'})).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('offers no bell to gateway_admin, which the endpoint rejects', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({data: []}))
    vi.stubGlobal('fetch', fetchMock)
    renderShell(['gateway_admin'])

    expect(await screen.findByText('page content')).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: '系统通知'})).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('reports a failed read instead of showing an empty feed', async () => {
    // An empty state would say "nothing happened", which is a different claim
    // from "we could not ask". A 500 must not be dressed up as calm.
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse({error: {message: 'database unavailable'}}, 500)),
    )
    renderShell(['auditor'])

    await userEvent.click(await screen.findByRole('button', {name: '系统通知'}))

    expect(await screen.findByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByText('暂无安全事件')).not.toBeInTheDocument()
  })
})

/**
 * The personal workspace has its own navigation over the same Shell. It must
 * not offer an administrator entry even when the account holds an
 * administrator role, and the administrator feed must not be fetched there.
 */
describe('Shell personal workspace navigation', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders the personal entries and no administrator entries', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({data: []}))
    vi.stubGlobal('fetch', fetchMock)
    renderShell(['member'], 'console')

    for (const label of ['概览', 'API Key', '模型', '用量', '调用日志', '个人资料', '安全']) {
      expect(await screen.findByRole('button', {name: label})).toBeInTheDocument()
    }
    for (const label of ['服务概览', '上游渠道', '对外模型', '路由策略', '用户', '系统设置', '审计日志']) {
      expect(screen.queryByRole('button', {name: label})).not.toBeInTheDocument()
    }
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('keeps the personal navigation for an administrator visiting /console', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    renderShell(['super_admin'], 'console')

    expect(await screen.findByRole('button', {name: '概览'})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: '用户'})).not.toBeInTheDocument()
    // The security feed is an administrator surface, not part of this workspace.
    expect(screen.queryByRole('button', {name: '系统通知'})).not.toBeInTheDocument()
  })
})

/**
 * The user menu is the shell's only disclosure that is not a dialog. It has to
 * open on the trigger, close on an outside click or a second press, and leave
 * its items as real buttons with visible labels rather than bare icons.
 */
describe('Shell user menu', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('opens on the trigger, exposes its items, and closes again', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    renderShell(['super_admin'])

    const trigger = screen.getByRole('button', {name: /ops/})
    expect(trigger).toHaveAttribute('aria-expanded', 'false')

    await userEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('menu')).toBeInTheDocument()
    expect(screen.getByRole('menuitem', {name: '个人资料'})).toBeInTheDocument()
    expect(screen.getByRole('menuitem', {name: '安全与访问'})).toBeInTheDocument()

    await userEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('closes when the operator clicks elsewhere on the page', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    renderShell(['super_admin'])

    await userEvent.click(screen.getByRole('button', {name: /ops/}))
    expect(screen.getByRole('menu')).toBeInTheDocument()

    await userEvent.click(screen.getByText('page content'))
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })
})

describe('Shell mobile navigation keyboard', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('contains focus while open and returns to the menu trigger after Escape', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({data: []})))
    vi.stubGlobal('matchMedia', vi.fn(() => ({matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn()})))
    const user = userEvent.setup()
    renderShell(['member'], 'console')
    const trigger = screen.getByRole('button', {name: '打开导航菜单'})
    await user.click(trigger)
    const panel = screen.getByRole('dialog', {name: '工作区导航'})
    expect(panel).toHaveFocus()
    await user.tab({shift: true})
    expect(screen.getByRole('button', {name: '收起侧栏'})).toHaveFocus()
    await user.tab()
    expect(panel).toContainElement(document.activeElement as HTMLElement)
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', {name: '工作区导航'})).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })
})
