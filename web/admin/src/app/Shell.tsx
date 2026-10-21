import {useCallback, useEffect, useRef, useState, type ReactNode} from 'react'
import {
  Bell,
  CheckCircle2,
  ChevronDown,
  ChevronsLeft,
  ChevronsRight,
  HelpCircle,
  Info,
  Loader2,
  LogOut,
  Menu,
  Moon,
  Search,
  ShieldCheck,
  Sun,
  UserRound,
  X,
} from 'lucide-react'
import {navGroups, pageMeta, type NavItem, type Page} from './navigation'
import {primaryRole, roleLabel} from '../lib/roles'
import type {WhoAmI} from '../api'
import {CampusSidebarCard} from '../components/ui/CampusIllustrations'
import {QuickSearchModal} from '../components/ui/QuickSearch'
import {Modal} from '../components/ui/Modal'

type Props = {
  page: Page
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

function visibleItems(roles: string[]): NavItem[] {
  return navGroups
    .flatMap(g => g.items)
    .filter(item => roles.some(r => item.roles.includes(r)))
}

export function Shell({
  page,
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
  const meta = pageMeta[page]
  const items = visibleItems(who?.roles || [])
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [userMenuOpen, setUserMenuOpen] = useState(false)
  const [notificationsOpen, setNotificationsOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const userMenuRef = useRef<HTMLDivElement>(null)
  // Stable identity: Modal re-runs its focus/restore effect when onClose
  // changes, and a new callback on every shell render would pull focus out of
  // the search input.
  const closeSearch = useCallback(() => setSearchOpen(false), [])

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
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMobileNavOpen(false)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
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
    const wide = window.matchMedia('(min-width: 960px)')
    const closeIfWide = () => {
      if (wide.matches) setMobileNavOpen(false)
    }
    closeIfWide()
    wide.addEventListener('change', closeIfWide)
    return () => wide.removeEventListener('change', closeIfWide)
  }, [mobileNavOpen])

  const groups = navGroups
    .map(g => ({...g, items: g.items.filter(i => items.includes(i))}))
    .filter(g => g.items.length > 0)

  const username = who?.username || who?.user || '管理员'
  const roles = who?.roles || []
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
      <aside id="admin-sidebar" className={`sidebar ${collapsed ? 'collapsed' : ''} ${mobileNavOpen ? 'mobileShown' : ''}`}>
        <div className="brand">
          <div className="brandLeft">
            <div className="brandMark" aria-hidden>
              AI
            </div>
            {(!collapsed || mobileNavOpen) && (
              <div className="brandText">
                <b>校园模型网关</b>
                <span>Campus AI Gateway</span>
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

        {/* Bottom promo illustration card (visible when expanded) */}
        {(!collapsed || mobileNavOpen) && <CampusSidebarCard />}

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
              <span className="searchBarPlaceholder">搜索页面、渠道或模型...</span>
              <kbd className="searchBarKbd">Ctrl K</kbd>
            </button>

            {refreshing && (
              <span className="refreshHint" role="status" aria-label="正在刷新">
                <Loader2 className="spin" aria-hidden />
              </span>
            )}

            {/* Notification Bell */}
            <div className="topbarActionItem">
              <button
                type="button"
                className="topbarIconBtn hasBadge"
                onClick={() => setNotificationsOpen(true)}
                title="系统通知"
                aria-label="系统通知"
              >
                <Bell aria-hidden />
                <span className="badgeDot" aria-hidden />
              </button>
            </div>

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
        />
      )}

      {/* System Notifications Modal */}
      {notificationsOpen && (
        <Modal
          title="系统通知"
          subtitle="网关运行状况与安全提示"
          onClose={() => setNotificationsOpen(false)}
        >
          <div className="notificationList">
            <div className="notificationItem unread">
              <div className="notificationIcon info">
                <Info aria-hidden />
              </div>
              <div className="notificationContent">
                <b>校园 AI 模型网关控制台已就绪</b>
                <p>已成功加载最新路由拓扑与渠道配置，所有对外模型状态正常。</p>
                <small className="notificationTime">刚刚</small>
              </div>
            </div>
            <div className="notificationItem">
              <div className="notificationIcon success">
                <CheckCircle2 aria-hidden />
              </div>
              <div className="notificationContent">
                <b>健康巡检正常</b>
                <p>Gateway 数据面、PostgreSQL 存储与 Redis 配额服务均处于健康状态。</p>
                <small className="notificationTime">今日 09:00</small>
              </div>
            </div>
          </div>
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
              <pre className="helpCodeBlock">
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
