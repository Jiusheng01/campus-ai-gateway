import {
  BarChart3,
  Boxes,
  FileClock,
  KeyRound,
  LayoutDashboard,
  Network,
  Route,
  ScrollText,
  Settings,
  ShieldAlert,
  ShieldCheck,
  UserRound,
  type LucideIcon,
} from 'lucide-react'

export type Page =
  | 'dashboard'
  | 'analytics'
  | 'overview'
  | 'usage'
  | 'keys'
  | 'logs'
  | 'audit'
  | 'security'
  | 'providers'
  | 'models'
  | 'routes'
  | 'calls'
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

// Entries are grouped by the business area the operator is working in. The old
// page ids (`keys`, `tenants`, `logs`, etc.) remain valid aliases in App.tsx so
// an existing bookmark or in-app link still lands somewhere real.
export const navGroups: NavGroup[] = [
  {
    id: 'model-service',
    label: '模型服务',
    items: [
      {id: 'dashboard', label: '服务概览', icon: LayoutDashboard, roles: MANAGEMENT},
      {id: 'providers', label: '上游渠道', icon: Network, roles: ['super_admin', 'gateway_admin']},
      {id: 'models', label: '对外模型', icon: Boxes, roles: ['super_admin', 'gateway_admin']},
      {id: 'routes', label: '路由策略', icon: Route, roles: ['super_admin', 'gateway_admin']},
      {id: 'calls', label: '调用管理', icon: KeyRound, roles: ['super_admin']},
      // Raw logs stay restricted: gateway_admin uses the scoped runtime summary
      // on the channel/model pages instead.
      {id: 'logs', label: '调用日志', icon: FileClock, roles: ['super_admin', 'auditor']},
    ],
  },
  {
    id: 'governance',
    label: '治理与审计',
    items: [
      {id: 'analytics', label: '数据看板', icon: BarChart3, roles: MANAGEMENT},
      {id: 'audit', label: '审计日志', icon: ScrollText, roles: ['super_admin', 'auditor']},
      {id: 'security', label: '安全事件', icon: ShieldAlert, roles: ['super_admin', 'auditor']},
    ],
  },
  {
    id: 'people',
    label: '用户与租户',
    items: [{id: 'users', label: '用户', icon: UserRound, roles: ['super_admin']}],
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

/**
 * The personal workspace's navigation. It is a second configuration over the
 * same Shell, not a filtered copy of the administrator's: an account in
 * /console must never see a channel, route or tenant-management entry even if
 * it happens to hold an administrator role.
 */
export const consoleNavGroups: NavGroup[] = [
  {
    id: 'personal-console',
    label: '个人控制台',
    items: [
      {id: 'overview', label: '概览', icon: LayoutDashboard, roles: EVERYONE},
      {id: 'keys', label: 'API Key', icon: KeyRound, roles: EVERYONE},
      {id: 'models', label: '模型', icon: Boxes, roles: EVERYONE},
      {id: 'usage', label: '用量', icon: BarChart3, roles: EVERYONE},
      {id: 'logs', label: '调用日志', icon: FileClock, roles: EVERYONE},
    ],
  },
  {
    id: 'personal-account',
    label: '个人',
    items: [
      {id: 'profile', label: '个人资料', icon: UserRound, roles: EVERYONE},
      {id: 'access', label: '安全', icon: ShieldCheck, roles: EVERYONE},
    ],
  },
]

/**
 * Pages addressable under each workspace prefix. The resolver rejects anything
 * else, so a typo in a URL is a not-found rather than an arbitrary page.
 */
export const adminRoutePages: Page[] = [
  'dashboard',
  'analytics',
  'keys',
  'logs',
  'audit',
  'security',
  'providers',
  'models',
  'routes',
  'calls',
  'users',
  'tenants',
  'settings',
  'profile',
  'access',
]

export const consoleRoutePages: Page[] = ['overview', 'keys', 'models', 'usage', 'logs', 'profile', 'access']

/** Title and one-line context for the topbar. */
export const pageMeta: Record<Page, {title: string; subtitle: string}> = {
  dashboard: {title: '服务概览', subtitle: '模型服务的运行状态与配置概况'},
  overview: {title: '概览', subtitle: '账号、租户与个人控制台状态'},
  usage: {title: '用量', subtitle: '你的请求、Tokens 与调用趋势'},
  analytics: {title: '数据看板', subtitle: '请求量、Tokens 与延迟的时间分布'},
  keys: {title: '调用管理', subtitle: '管理租户配额与业务调用凭据'},
  logs: {title: '调用日志', subtitle: '检索与排查业务请求'},
  audit: {title: '审计日志', subtitle: '管理员对控制面的操作记录'},
  security: {title: '安全事件', subtitle: '鉴权、权限与配额拒绝事件'},
  providers: {title: '上游渠道', subtitle: '管理上游渠道的接入、凭据与候选模型'},
  models: {title: '对外模型', subtitle: '管理业务系统调用的模型名称与线路配置'},
  routes: {title: '路由策略', subtitle: '查看和编辑所有对外模型的线路'},
  calls: {title: '调用管理', subtitle: '管理租户配额与业务调用凭据'},
  users: {title: '用户', subtitle: '管理注册用户和管理员角色'},
  tenants: {title: '调用管理', subtitle: '管理租户配额与业务调用凭据'},
  settings: {title: '系统设置', subtitle: '认证方式与运行参数'},
  profile: {title: '个人资料', subtitle: '账号身份与权限范围'},
  access: {title: '安全与访问', subtitle: '密码与登录设备'},
}

/**
 * The personal workspace has its own titles for the pages it shares with the
 * administrator console. It is deliberately partial: the Shell falls back to
 * pageMeta for anything not listed, but the resolver only admits
 * consoleRoutePages here.
 */
export const consolePageMeta: Partial<Record<Page, {title: string; subtitle: string}>> = {
  overview: {title: '概览', subtitle: '你的账号、租户与工作台状态'},
  keys: {title: 'API Key', subtitle: '管理属于你自己的调用凭据'},
  models: {title: '模型', subtitle: '你的租户允许使用的模型'},
  usage: {title: '用量', subtitle: '你的请求、Tokens 与调用趋势'},
  logs: {title: '调用日志', subtitle: '你自己发起的请求记录'},
  profile: {title: '个人资料', subtitle: '账号身份与权限范围'},
  access: {title: '安全', subtitle: '密码与登录设备'},
}

export const allNavItems: NavItem[] = navGroups.flatMap(g => g.items)

/** Pages the account may enter, in navigation order. */
export function allowedPages(roles: string[]): Page[] {
  const pages = allNavItems.filter(item => roleGranted(item.roles, roles)).map(item => item.id)
  // `keys` and `tenants` are legacy page ids that now land on 调用管理. They
  // are not separate navigation entries, but an existing link must not be
  // redirected away by the role guard.
  if (pages.includes('calls')) pages.push('keys', 'tenants')
  return pages
}

// roleGranted is permissive about an account with no roles at all: the gateway
// treats it as a member, so the navigation has to agree.
function roleGranted(allowed: string[], roles: string[]): boolean {
  if (!roles.length) return allowed.includes('member')
  return roles.some(r => allowed.includes(r))
}
