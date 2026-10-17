import {
  BarChart3,
  FileClock,
  KeyRound,
  LayoutDashboard,
  ListChecks,
  Network,
  Route,
  ScrollText,
  Settings,
  ShieldAlert,
  ShieldCheck,
  UserRound,
  Users,
  type LucideIcon,
} from 'lucide-react'

export type Page =
  | 'dashboard'
  | 'analytics'
  | 'keys'
  | 'logs'
  | 'audit'
  | 'security'
  | 'tasks'
  | 'providers'
  | 'models'
  | 'users'
  | 'tenants'
  | 'settings'
  | 'profile'
  | 'access'

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

// MANAGEMENT mirrors the gateway's allow-list for the pages every
// administrator may reach. tenant_admin is deliberately absent: it is inert
// until the scoped tenant membership exists, so offering it here would only
// lead to a 403.
const MANAGEMENT = ['super_admin', 'gateway_admin', 'auditor', 'operator']

// Anyone signed in has a profile and can manage their own password and devices.
const EVERYONE = [...MANAGEMENT, 'member']

// Entries are grouped by what the operator is trying to do. A group disappears
// when none of its items survive filtering, so a member does not end up staring
// at an empty heading.
//
// The order is the order the work happens in: read the state, read the trend,
// then the records behind both, then the configuration that produced them.
export const navGroups: NavGroup[] = [
  {
    id: 'overview',
    label: '概览',
    items: [
      {id: 'dashboard', label: '总览', icon: LayoutDashboard, roles: MANAGEMENT},
      {id: 'analytics', label: '数据看板', icon: BarChart3, roles: MANAGEMENT},
      {id: 'keys', label: 'API 密钥', icon: KeyRound, roles: ['super_admin']},
      {id: 'logs', label: '使用日志', icon: FileClock, roles: ['super_admin', 'auditor']},
      {id: 'audit', label: '审计日志', icon: ScrollText, roles: ['super_admin', 'auditor']},
      {id: 'security', label: '安全事件', icon: ShieldAlert, roles: ['super_admin', 'auditor']},
      {id: 'tasks', label: '任务日志', icon: ListChecks, roles: ['super_admin', 'auditor']},
    ],
  },
  {
    id: 'models',
    label: '模型服务',
    items: [
      {id: 'providers', label: '渠道', icon: Network, roles: ['super_admin', 'gateway_admin']},
      {id: 'models', label: '模型与路由', icon: Route, roles: ['super_admin', 'gateway_admin']},
    ],
  },
  {
    id: 'people',
    label: '用户与租户',
    items: [
      {id: 'users', label: '用户', icon: UserRound, roles: ['super_admin']},
      {id: 'tenants', label: '租户', icon: Users, roles: ['super_admin']},
    ],
  },
  {
    id: 'system',
    label: '系统',
    items: [{id: 'settings', label: '系统设置', icon: Settings, roles: MANAGEMENT}],
  },
  {
    id: 'personal',
    label: '个人',
    items: [
      {id: 'profile', label: '个人资料', icon: UserRound, roles: EVERYONE},
      {id: 'access', label: '安全与访问', icon: ShieldCheck, roles: EVERYONE},
    ],
  },
]

/** Title and one-line context for the topbar. */
export const pageMeta: Record<Page, {title: string; subtitle: string}> = {
  dashboard: {title: '总览', subtitle: '校园 AI 基础设施运行状态'},
  analytics: {title: '数据看板', subtitle: '请求量、Tokens 与延迟的时间分布'},
  keys: {title: 'API 密钥', subtitle: '管理业务系统调用凭证'},
  logs: {title: '使用日志', subtitle: '检索与排查业务请求'},
  audit: {title: '审计日志', subtitle: '管理员对控制面的操作记录'},
  security: {title: '安全事件', subtitle: '鉴权、权限与配额拒绝事件'},
  tasks: {title: '任务日志', subtitle: '异步任务的提交与执行记录'},
  providers: {title: '渠道', subtitle: '管理模型渠道的接入与凭据来源'},
  models: {title: '模型与路由', subtitle: '管理对外模型与上游线路'},
  users: {title: '用户', subtitle: '管理注册用户和管理员角色'},
  tenants: {title: '租户', subtitle: '管理租户与配额边界'},
  settings: {title: '系统设置', subtitle: '认证方式与运行参数'},
  profile: {title: '个人资料', subtitle: '账号身份与权限范围'},
  access: {title: '安全与访问', subtitle: '密码与登录设备'},
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
  return allowed[0] || 'profile'
}

// roleGranted is permissive about an account with no roles at all: the gateway
// treats it as a member, so the navigation has to agree.
function roleGranted(allowed: string[], roles: string[]): boolean {
  if (!roles.length) return allowed.includes('member')
  return roles.some(r => allowed.includes(r))
}
