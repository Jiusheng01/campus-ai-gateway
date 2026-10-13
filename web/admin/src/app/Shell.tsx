import type {ReactNode} from 'react'
import {Loader2, LogOut, Menu, Moon, Sun} from 'lucide-react'
import {IconButton} from '../components/ui/Button'
import {navGroups, pageMeta, type Page} from './navigation'
import type {WhoAmI} from '../api'

const ROLE_LABELS: Record<string, string> = {
  super_admin: '超级管理员',
  gateway_admin: '网关管理员',
  tenant_admin: '租户管理员',
  auditor: '审计员',
  operator: '运维人员',
}

const roleLabel = (r: string) => ROLE_LABELS[r] || r

/**
 * The identity chip shows one role plus a count.
 *
 * Five role strings in the topbar is unreadable and pushes the theme and
 * logout controls around as the width changes.
 */
function IdentityChip({who}: {who: WhoAmI | null}) {
  if (!who) return null
  const name = who.username || who.user || '未知用户'
  const roles = who.roles || []
  const primary = roles[0]
  return (
    <span className="identity" title={roles.map(roleLabel).join('、')}>
      <b className="identityName">{name}</b>
      <small className="identityRole">
        {primary ? roleLabel(primary) : '无角色'}
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

  return (
    <div className={`shell ${collapsed ? 'isCollapsed' : ''}`}>
      <aside className={`sidebar ${collapsed ? 'collapsed' : ''}`}>
        <div className="brand">
          <div className="brandMark" aria-hidden>
            AI
          </div>
          {!collapsed && (
            <div className="brandText">
              <b>校园模型网关</b>
              <span>Campus AI Gateway</span>
            </div>
          )}
        </div>

        <nav className="nav" aria-label="主导航">
          {navGroups.map(group => (
            <div className="navGroup" key={group.id}>
              {!collapsed && <span className="navGroupLabel">{group.label}</span>}
              {collapsed && <span className="navGroupRule" aria-hidden />}
              {group.items.map(item => {
                const Icon = item.icon
                const active = page === item.id
                return (
                  <button
                    key={item.id}
                    type="button"
                    className={`navItem ${active ? 'active' : ''}`}
                    onClick={() => onNavigate(item.id)}
                    title={collapsed ? item.label : undefined}
                    aria-current={active ? 'page' : undefined}
                  >
                    <Icon aria-hidden />
                    {!collapsed && <span>{item.label}</span>}
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
          <div className="topbarText">
            <h1>{meta.title}</h1>
            <p>{meta.subtitle}</p>
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
