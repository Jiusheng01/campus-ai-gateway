import {useCallback, useEffect, useRef, useState, type ReactNode} from 'react'
import {
  Bell,
  ChevronDown,
  ChevronsLeft,
  ChevronsRight,
  HelpCircle,
  Loader2,
  LogOut,
  Menu,
  Moon,
  Search,
  ShieldAlert,
  ShieldCheck,
  Sun,
  UserRound,
  X,
} from 'lucide-react'
import {consoleNavGroups, consolePageMeta, navGroups, pageMeta, type NavItem, type Page} from './navigation'
import type {Workspace} from './routing'
import {primaryRole, roleLabel} from '../lib/roles'
import {api, type SecurityEvent, type WhoAmI} from '../api'
import {containTab} from '../components/ui/focus'
import {QuickSearchModal} from '../components/ui/QuickSearch'
import {Modal} from '../components/ui/Modal'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {useResource} from '../lib/useResource'
import {securityLabel, securityTone} from '../lib/labels'
import {ago} from '../lib/format'

/**
 * Width at which the sidebar becomes an overlay drawer, read from the desktop
 * side for the resize-close listener. It has to equal the breakpoint of the
 * `.sidebar` / `.mobileNavTrigger` rules in styles.css: the drawer state and
 * the layout that shows it are two halves of one switch, and a mismatch leaves
 * the drawer openable at a width where no drawer exists.
 */
const DESKTOP_QUERY = '(min-width: 781px)'

/** Roles the gateway accepts on GET /admin/security-events (see the RBAC allow-list). */
const mayReadSecurityEvents = (roles: string[]) =>
  roles.includes('super_admin') || roles.includes('auditor')

type Props = {
  page: Page
  /** Which workspace the Shell is rendering. Defaults to the admin console. */
  workspace?: Workspace
  onNavigate: (p: Page) => void
  collapsed: boolean
  onToggleCollapse: () => void
  dark: boolean
  onToggleTheme: () => void
  who: WhoAmI | null
  sessionMode: boolean
  refreshing: boolean
  onSignOut: () => void
  children: ReactNode
}

function visibleItems(groups: typeof navGroups, roles: string[]): NavItem[] {
  return groups
    .flatMap(g => g.items)
    .filter(item => roles.some(r => item.roles.includes(r)))
}

export function Shell({
  page,
  workspace = 'admin',
  onNavigate,
  collapsed,
  onToggleCollapse,
  dark,
  onToggleTheme,
  who,
  sessionMode,
  refreshing,
  onSignOut,
  children,
}: Props) {
  const isPersonal = workspace === 'console'
  const navSource = isPersonal ? consoleNavGroups : navGroups
  const meta = (isPersonal ? consolePageMeta[page] : undefined) ?? pageMeta[page]
  const items = visibleItems(navSource, who?.roles || [])
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [userMenuOpen, setUserMenuOpen] = useState(false)
  const [notificationsOpen, setNotificationsOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const userMenuRef = useRef<HTMLDivElement>(null)
  const sidebarRef = useRef<HTMLElement>(null)
  // Stable identity: Modal re-runs its focus/restore effect when onClose
  // changes, and a new callback on every shell render would pull focus out of
  // the search input.
  const closeSearch = useCallback(() => setSearchOpen(false), [])

  const roles = who?.roles || []
  // The security feed is an administrator surface. Even an administrator
  // visiting /console must not see it in the personal workspace.
  const canReadSecurity = !isPersonal && mayReadSecurityEvents(roles)

  // Only fetched for roles the endpoint accepts. Asking with any other role
  // would 403 on every page load and light the bell for an error the operator
  // cannot act on. The resource is shared by the dot and the panel so the two
  // cannot describe different event lists.
  const security = useResource(
    async () => {
      if (!canReadSecurity) return []
      const d = await api<{data: SecurityEvent[]}>(`/admin/security-events?limit=5`)
      return d.data || []
    },
    [canReadSecurity],
  )
  const events = security.data || []

  // The dot marks events nobody has opened the panel for. It is derived from
  // the response rather than a static class: an empty deployment has nothing to
  // point at, and "已读" is a fact about this browser session, not the gateway.
  const [seenEventIds, setSeenEventIds] = useState<Set<number>>(new Set())
  const unreadCount = events.filter(e => !seenEventIds.has(e.id)).length

  function openNotifications() {
    setNotificationsOpen(true)
    setSeenEventIds(prev => {
      const next = new Set(prev)
      for (const event of events) next.add(event.id)
      return next
    })
  }

  // Listen for Ctrl+K or Cmd+K
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setSearchOpen(prev => !prev)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  // Close user menu on outside click
  useEffect(() => {
    if (!userMenuOpen) return
    const onClick = (e: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [userMenuOpen])

  // Close mobile drawer on Escape key
  useEffect(() => {
    if (!mobileNavOpen) return
    const restoreTo = document.activeElement as HTMLElement | null
    sidebarRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMobileNavOpen(false)
      if (sidebarRef.current) containTab(e, sidebarRef.current)
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      restoreTo?.focus()
    }
  }, [mobileNavOpen])

  // Prevent background scroll while mobile drawer is open
  useEffect(() => {
    if (mobileNavOpen) {
      document.body.style.overflow = 'hidden'
    } else {
      document.body.style.overflow = ''
    }
    return () => {
      document.body.style.overflow = ''
    }
  }, [mobileNavOpen])

  // Close mobile drawer when resizing to desktop
  useEffect(() => {
    if (!mobileNavOpen) return
    const wide = window.matchMedia(DESKTOP_QUERY)
    const closeIfWide = () => {
      if (wide.matches) setMobileNavOpen(false)
    }
    closeIfWide()
    wide.addEventListener('change', closeIfWide)
    return () => wide.removeEventListener('change', closeIfWide)
  }, [mobileNavOpen])

  const groups = navSource
    .map(g => ({...g, items: g.items.filter(i => items.includes(i))}))
    .filter(g => g.items.length > 0)

  const username = who?.username || who?.user || '管理员'
  const userRoleText = roleLabel(primaryRole(roles))

  return (
    <div className={`shell ${collapsed ? 'isCollapsed' : ''} ${mobileNavOpen ? 'isMobileOpen' : ''}`}>
      {mobileNavOpen && (
        <div
          className="sidebarBackdrop"
          aria-hidden="true"
          onClick={() => setMobileNavOpen(false)}
        />
      )}

      {/* Main Left Sidebar */}
      <aside
        id="admin-sidebar"
        ref={sidebarRef}
        tabIndex={-1}
        role={mobileNavOpen ? 'dialog' : undefined}
        aria-modal={mobileNavOpen ? true : undefined}
        aria-label={mobileNavOpen ? '工作区导航' : undefined}
        className={`sidebar ${collapsed ? 'collapsed' : ''} ${mobileNavOpen ? 'mobileShown' : ''}`}
      >
        <div className="brand">
          <div className="brandLeft">
            <div className="brandMark" aria-hidden>
              AI
            </div>
            {(!collapsed || mobileNavOpen) && (
              <div className="brandText">
                <b>校园模型网关</b>
                <span>{isPersonal ? '个人工作台' : '管理工作台'}</span>
              </div>
            )}
          </div>
          {(!collapsed || mobileNavOpen) && (
            <button
              type="button"
              className="brandCollapseBtn"
              onClick={onToggleCollapse}
              title="折叠侧栏"
              aria-label="折叠侧栏"
            >
              <ChevronsLeft aria-hidden />
            </button>
          )}
          {mobileNavOpen && (
            <button
              type="button"
              className="iconBtn mobileCloseBtn"
              onClick={() => setMobileNavOpen(false)}
              aria-label="关闭导航菜单"
            >
              <X aria-hidden />
            </button>
          )}
        </div>

        <nav className="nav" aria-label="主导航">
          {groups.map(group => (
            <div className="navGroup" key={group.id}>
              {(!collapsed || mobileNavOpen) && <span className="navGroupLabel">{group.label}</span>}
              {collapsed && !mobileNavOpen && <span className="navGroupRule" aria-hidden />}
              {group.items.map(item => {
                const Icon = item.icon
                const active = page === item.id
                return (
                  <button
                    key={item.id}
                    type="button"
                    className={`navItem ${active ? 'active' : ''}`}
                    onClick={() => {
                      onNavigate(item.id)
                      setMobileNavOpen(false)
                    }}
                    title={collapsed && !mobileNavOpen ? item.label : undefined}
                    aria-current={active ? 'page' : undefined}
                  >
                    <Icon className="navItemIcon" aria-hidden />
                    {(!collapsed || mobileNavOpen) && <span className="navItemLabel">{item.label}</span>}
                  </button>
                )
              })}
            </div>
          ))}
        </nav>

        {/* Sidebar Collapse Toggle at Bottom */}
        <button
          type="button"
          className="collapseBtn"
          onClick={onToggleCollapse}
          title={collapsed ? '展开侧栏' : '收起侧栏'}
          aria-label={collapsed ? '展开侧栏' : '收起侧栏'}
        >
          {collapsed ? <ChevronsRight aria-hidden /> : <ChevronsLeft aria-hidden />}
          {!collapsed && <span>收起侧栏</span>}
        </button>
      </aside>

      {/* Main Workspace Area */}
      <section className="workspace">
        {/* Top Header Bar */}
        <header className="topbar">
          <div className="topbarLeft">
            <button
              type="button"
              className="iconBtn mobileNavTrigger"
              onClick={() => setMobileNavOpen(true)}
              aria-label="打开导航菜单"
              aria-controls="admin-sidebar"
              aria-expanded={mobileNavOpen}
              title="打开菜单"
            >
              <Menu aria-hidden />
            </button>
            <div className="topbarText">
              <h1>{meta.title}</h1>
              <p>{meta.subtitle}</p>
            </div>
          </div>

          <div className="topbarRight">
            {/* Quick Search Bar trigger */}
            <button
              type="button"
              className="topbarSearchBar"
              onClick={() => setSearchOpen(true)}
              aria-label="全局搜索"
              aria-haspopup="dialog"
            >
              <Search className="searchBarIcon" aria-hidden />
              <span className="searchBarPlaceholder">
                {isPersonal ? '搜索个人控制台页面...' : '搜索页面、渠道或模型...'}
              </span>
              <kbd className="searchBarKbd">Ctrl K</kbd>
            </button>

            {refreshing && (
              <span className="refreshHint" role="status" aria-label="正在刷新">
                <Loader2 className="spin" aria-hidden />
              </span>
            )}

            {/* Notification Bell. Hidden for roles the endpoint would reject. */}
            {canReadSecurity && (
              <div className="topbarActionItem">
                <button
                  type="button"
                  className={`topbarIconBtn ${unreadCount ? 'hasBadge' : ''}`}
                  onClick={openNotifications}
                  title="安全事件"
                  aria-label="系统通知"
                >
                  <Bell aria-hidden />
                  {unreadCount > 0 && <span className="badgeDot" aria-hidden />}
                </button>
              </div>
            )}

            {/* Help Question Icon */}
            <div className="topbarActionItem">
              <button
                type="button"
                className="topbarIconBtn"
                onClick={() => setHelpOpen(true)}
                title="帮助与说明"
                aria-label="帮助与说明"
              >
                <HelpCircle aria-hidden />
              </button>
            </div>

            {/* Theme Toggle */}
            <div className="topbarActionItem">
              <button
                type="button"
                className="topbarIconBtn"
                onClick={onToggleTheme}
                title={dark ? '切换到浅色主题' : '切换到深色主题'}
                aria-label={dark ? '切换到浅色主题' : '切换到深色主题'}
              >
                {dark ? <Sun aria-hidden /> : <Moon aria-hidden />}
              </button>
            </div>

            {/* User Profile Menu */}
            <div className="userMenuWrapper" ref={userMenuRef}>
              <button
                type="button"
                className={`userMenuTrigger ${userMenuOpen ? 'open' : ''}`}
                onClick={() => setUserMenuOpen(prev => !prev)}
                aria-expanded={userMenuOpen}
                aria-haspopup="menu"
                title={`${username} (${userRoleText})`}
              >
                <span className="userAvatar" aria-hidden>
                  {username.charAt(0).toUpperCase()}
                </span>
                <div className="userMeta">
                  <b className="userName">{username}</b>
                  <span className="userRole">{userRoleText}</span>
                </div>
                <ChevronDown className="userChevron" aria-hidden />
              </button>

              {userMenuOpen && (
                <div className="userDropdown" role="menu">
                  <div className="userDropdownHead">
                    <b className="dropdownUsername">{username}</b>
                    <span className="dropdownRoles">
                      {roles.map(roleLabel).join(' · ') || userRoleText}
                    </span>
                  </div>
                  <div className="userDropdownDivider" />
                  <button
                    type="button"
                    className="userDropdownItem"
                    role="menuitem"
                    onClick={() => {
                      setUserMenuOpen(false)
                      onNavigate('profile')
                    }}
                  >
                    <UserRound aria-hidden />
                    <span>个人资料</span>
                  </button>
                  <button
                    type="button"
                    className="userDropdownItem"
                    role="menuitem"
                    onClick={() => {
                      setUserMenuOpen(false)
                      onNavigate('access')
                    }}
                  >
                    <ShieldCheck aria-hidden />
                    <span>安全与访问</span>
                  </button>
                  {sessionMode && (
                    <>
                      <div className="userDropdownDivider" />
                      <button
                        type="button"
                        className="userDropdownItem danger"
                        role="menuitem"
                        onClick={() => {
                          setUserMenuOpen(false)
                          onSignOut()
                        }}
                      >
                        <LogOut aria-hidden />
                        <span>退出登录</span>
                      </button>
                    </>
                  )}
                </div>
              )}
            </div>
          </div>
        </header>

        {/* Page Content Viewport */}
        <main className="page">{children}</main>
      </section>

      {/* Global Quick Search Modal. Mounted only while open so its state and
          the dialog's captured focus start fresh on every invocation. */}
      {searchOpen && (
        <QuickSearchModal
          onClose={closeSearch}
          onNavigate={onNavigate}
          roles={roles}
          workspace={workspace}
        />
      )}

      {/* Recent security events. The gateway has no notification endpoint, so
          this panel shows the one feed it does supply rather than inventing
          messages: every row below comes from /admin/security-events, and with
          no rows the panel says so. */}
      {notificationsOpen && canReadSecurity && (
        <Modal
          title="安全事件"
          subtitle="最近 5 条鉴权、权限与配额拒绝事件"
          onClose={() => setNotificationsOpen(false)}
        >
          {security.loading && !security.data ? (
            <SkeletonRows rows={3} columns={2} />
          ) : security.error ? (
            <ErrorState message={security.error} status={security.errorStatus} onRetry={security.reload} />
          ) : events.length ? (
            <div className="notificationList">
              {events.map(event => (
                <div className="notificationItem" key={event.id}>
                  <div className={`notificationIcon ${securityTone(event.event_type)}`}>
                    <ShieldAlert aria-hidden />
                  </div>
                  <div className="notificationContent">
                    <b>{securityLabel(event.event_type)}</b>
                    <p>
                      {event.detail || '—'}
                      {event.requested_model ? ` · ${event.requested_model}` : ''}
                      {event.client_ip ? ` · ${event.client_ip}` : ''}
                    </p>
                    <small className="notificationTime">{ago(event.created_at)}</small>
                  </div>
                </div>
              ))}
              <button
                type="button"
                className="link notificationMore"
                onClick={() => {
                  setNotificationsOpen(false)
                  onNavigate('security')
                }}
              >
                查看全部安全事件
              </button>
            </div>
          ) : (
            <EmptyState
              icon={<ShieldAlert />}
              title="暂无安全事件"
              description="发生鉴权失败或限流拒绝后，事件会记录在这里。"
            />
          )}
        </Modal>
      )}

      {/* Help Modal */}
      {helpOpen && (
        <Modal
          title="校园模型网关使用帮助"
          subtitle="统一模型服务与管理控制台指南"
          onClose={() => setHelpOpen(false)}
        >
          <div className="helpContent">
            <section className="helpSection">
              <h3>快速接入指南</h3>
              <p>业务系统通过标准 OpenAI 兼容接口调用网关：</p>
              <pre className="helpCodeBlock" tabIndex={0} role="region" aria-label="调用示例">
                <code>
                  {`POST /v1/chat/completions\nAuthorization: Bearer <API_KEY>\nContent-Type: application/json\n\n{\n  "model": "campus-gpt",\n  "messages": [{"role": "user", "content": "你好"}]\n}`}
                </code>
              </pre>
            </section>
            <section className="helpSection">
              <h3>快捷键提示</h3>
              <ul className="helpList">
                <li><kbd>Ctrl</kbd> + <kbd>K</kbd> / <kbd>Cmd</kbd> + <kbd>K</kbd>：打开全局快速搜索</li>
                <li><kbd>Esc</kbd>：关闭弹出面板与搜索框</li>
              </ul>
            </section>
          </div>
        </Modal>
      )}
    </div>
  )
}
