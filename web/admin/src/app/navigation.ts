import {
  Database,
  FileClock,
  KeyRound,
  LayoutDashboard,
  Network,
  Route,
  Settings,
  ShieldAlert,
  UserRound,
  Users,
  type LucideIcon,
} from 'lucide-react'

export type Page =
  | 'dashboard'
  | 'providers'
  | 'models'
  | 'tenants'
  | 'keys'
  | 'logs'
  | 'audit'
  | 'security'
  | 'users'
  | 'settings'
  | 'account'

export type NavItem = {
  id: Page
  label: string
  icon: LucideIcon
  /**
   * Roles that may see this entry. The lists mirror what the gateway's RBAC
   * allow-list actually permits, so the navigation never offers a page whose
   * only possible result is a 403. Hiding an entry is a convenience, not a
   * permission: the backend stays the real boundary.
   */
  roles: string[]
}

export type NavGroup = {id: string; label: string; items: NavItem[]}

const MANAGEMENT = ['super_admin', 'gateway_admin', 'tenant_admin', 'auditor', 'operator']

// Entries are grouped by what the operator is trying to do. A group disappears
// when none of its items survive filtering, so a member does not end up staring
// at an empty "模型服务" heading.
export const navGroups: NavGroup[] = [
  {
    id: 'overview',
    label: '概览',
    items: [{id: 'dashboard', label: '总览', icon: LayoutDashboard, roles: MANAGEMENT}],
  },
  {
    id: 'models',
    label: '模型服务',
    items: [
      {id: 'providers', label: '供应商', icon: Network, roles: ['super_admin', 'gateway_admin']},
      {id: 'models', label: '模型与路由', icon: Route, roles: ['super_admin', 'gateway_admin']},
    ],
  },
  {
    id: 'access',
    label: '访问管理',
    items: [
      {id: 'tenants', label: '租户', icon: Users, roles: ['super_admin', 'tenant_admin']},
      {id: 'keys', label: 'API Key', icon: KeyRound, roles: ['super_admin', 'tenant_admin']},
    ],
  },
  {
    id: 'observability',
    label: '可观测性',
    items: [
      {id: 'logs', label: '请求日志', icon: FileClock, roles: ['super_admin', 'auditor']},
      {id: 'audit', label: '管理审计', icon: Database, roles: ['super_admin', 'auditor']},
      {id: 'security', label: '安全事件', icon: ShieldAlert, roles: ['super_admin', 'auditor']},
    ],
  },
  {
    id: 'system',
    label: '系统',
    items: [
      {id: 'users', label: '用户与权限', icon: UserRound, roles: ['super_admin']},
      {id: 'settings', label: '系统设置', icon: Settings, roles: MANAGEMENT},
      {id: 'account', label: '我的账号', icon: UserRound, roles: ['member']},
    ],
  },
]

/** Title and one-line context for the topbar. */
export const pageMeta: Record<Page, {title: string; subtitle: string}> = {
  dashboard: {title: '总览', subtitle: '校园 AI 基础设施运行状态'},
  providers: {title: '供应商', subtitle: '管理模型供应商接入'},
  models: {title: '模型与路由', subtitle: '管理对外模型与上游线路'},
  tenants: {title: '租户', subtitle: '管理租户与配额边界'},
  keys: {title: 'API Key', subtitle: '管理业务系统调用凭证'},
  logs: {title: '请求日志', subtitle: '检索与排查业务请求'},
  audit: {title: '管理审计', subtitle: '管理员对控制面的操作记录'},
  security: {title: '安全事件', subtitle: '鉴权、权限与配额拒绝事件'},
  users: {title: '用户与权限', subtitle: '管理注册用户和管理员角色'},
  settings: {title: '系统设置', subtitle: '账号、认证方式与登录设备'},
  account: {title: '我的账号', subtitle: '账号状态与安全设置'},
}

export const allNavItems: NavItem[] = navGroups.flatMap(g => g.items)

/** Pages the account may enter, in navigation order. */
export function allowedPages(roles: string[]): Page[] {
  return allNavItems.filter(item => roleGranted(item.roles, roles)).map(item => item.id)
}

/** Where an account lands after signing in: the dashboard if it may see it, else the first page it may. */
export function landingPage(roles: string[]): Page {
  const allowed = allowedPages(roles)
  if (allowed.includes('dashboard')) return 'dashboard'
  return allowed[0] || 'account'
}

// roleGranted is permissive about an account with no roles at all: the gateway
// treats it as a member, so the navigation has to agree.
function roleGranted(allowed: string[], roles: string[]): boolean {
  if (!roles.length) return allowed.includes('member')
  return roles.some(r => allowed.includes(r))
}
