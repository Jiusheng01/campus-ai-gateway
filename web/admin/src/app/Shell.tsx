import {useEffect, useState, type ReactNode} from 'react'
import {Loader2, LogOut, Menu, Moon, Sun, X} from 'lucide-react'
import {IconButton} from '../components/ui/Button'
import {navGroups, pageMeta, type NavItem, type Page } from './navigation'
import {primaryRole, roleLabel} from '../lib/roles'
import type {WhoAmI} from '../api'

/**
 * The identity chip shows one role plus a count.
 *
 * Six role strings in the topbar is unreadable and pushes the theme and logout
 * controls around as the width changes.
 */
function IdentityChip({who}: {who: WhoAmI | null}) {
  if (!who) return null
  const name = who.username || who.user || '未知用户'
  const roles = who.roles || []
  return (
    <span className="identity" title={roles.map(roleLabel).join('、') || roleLabel('member')}>
      <b className="identityName">{name}</b>
      <small className="identityRole">
        {roleLabel(primaryRole(roles))}
        {roles.length > 1 && <span className="identityMore">+{roles.length - 1}</span>}
      </small>
    </span>
  )
}

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

// visibleItems is what the navigation shows. Hiding a page someone may not open
// is a courtesy: the gateway's allow-list is still what answers the request.
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

  // The drawer only exists in the narrow layout. If the viewport grows past the
  // breakpoint while it is open — a phone rotated to landscape, a desktop window
  // widened — the sidebar goes back to being fixed, but the scroll lock would
  // stay on and the close button would be stranded in a layout that has no
  // drawer. Close it on the way up rather than leaving the page unscrollable.
  useEffect(() => {
    if (!mobileNavOpen) return
    const wide = window.matchMedia('(min-width: 781px)')
    const closeIfWide = () => {
      if (wide.matches) setMobileNavOpen(false)
    }
    closeIfWide()
    wide.addEventListener('change', closeIfWide)
    return () => wide.removeEventListener('change', closeIfWide)
  }, [mobileNavOpen])

  // Group the visible items back into their headings, dropping any group that
  // has nothing left in it.
  const groups = navGroups
    .map(g => ({...g, items: g.items.filter(i => items.includes(i))}))
    .filter(g => g.items.length > 0)

  return (
    <div className={`shell ${collapsed ? 'isCollapsed' : ''} ${mobileNavOpen ? 'isMobileOpen' : ''}`}>
      {mobileNavOpen && (
        <div
          className="sidebarBackdrop"
          aria-hidden="true"
          onClick={() => setMobileNavOpen(false)}
        />
      )}

      <aside id="admin-sidebar" className={`sidebar ${collapsed ? 'collapsed' : ''} ${mobileNavOpen ? 'mobileShown' : ''}`}>
        <div className="brand">
          <div className="brandMark" aria-hidden>
            AI
          </div>
          {(!collapsed || mobileNavOpen) && (
            <div className="brandText">
              <b>校园模型网关</b>
              <span>Campus AI Gateway</span>
            </div>
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
                    <Icon aria-hidden />
                    {(!collapsed || mobileNavOpen) && <span>{item.label}</span>}
                  </button>
                )
              })}
            </div>
          ))}
        </nav>

        <button
          type="button"
          className="collapseBtn"
          onClick={onToggleCollapse}
          title={collapsed ? '展开侧栏' : '折叠侧栏'}
          aria-label={collapsed ? '展开侧栏' : '折叠侧栏'}
        >
          <Menu />
          {!collapsed && <span>折叠侧栏</span>}
        </button>
      </aside>

      <section className="workspace">
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
          <div className="topActions">
            {refreshing && (
              <span className="refreshHint" role="status" aria-label="正在刷新">
                <Loader2 className="spin" aria-hidden />
              </span>
            )}
            <IdentityChip who={who} />
            <IconButton
              label={dark ? '切换到浅色主题' : '切换到深色主题'}
              icon={dark ? <Sun /> : <Moon />}
              onClick={onToggleTheme}
            />
            {sessionMode && <IconButton label="退出登录" icon={<LogOut />} onClick={onSignOut} />}
          </div>
        </header>
        <main className="page">{children}</main>
      </section>
    </div>
  )
}
