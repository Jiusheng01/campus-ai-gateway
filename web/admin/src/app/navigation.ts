import {
  Database,
  FileClock,
  KeyRound,
  LayoutDashboard,
  Network,
  Route,
  Settings,
  ShieldAlert,
  Users,
  type LucideIcon,
} from 'lucide-react'

export type Page = 'dashboard' | 'providers' | 'models' | 'tenants' | 'keys' | 'logs' | 'audit' | 'security' | 'settings'

export type NavItem = {id: Page; label: string; icon: LucideIcon}

export type NavGroup = {id: string; label: string; items: NavItem[]}

// Nine flat entries force the eye to re-read the whole list on every visit.
// Grouping them by what the operator is trying to do makes the console
// scannable as it grows.
export const navGroups: NavGroup[] = [
  {id: 'overview', label: '概览', items: [{id: 'dashboard', label: '总览', icon: LayoutDashboard}]},
  {
    id: 'models',
    label: '模型服务',
    items: [
      {id: 'providers', label: '供应商', icon: Network},
      {id: 'models', label: '模型与路由', icon: Route},
    ],
  },
  {
    id: 'access',
    label: '访问管理',
    items: [
      {id: 'tenants', label: '租户', icon: Users},
      {id: 'keys', label: 'API Key', icon: KeyRound},
    ],
  },
  {
    id: 'observability',
    label: '可观测性',
    items: [
      {id: 'logs', label: '请求日志', icon: FileClock},
      {id: 'audit', label: '管理审计', icon: Database},
      {id: 'security', label: '安全事件', icon: ShieldAlert},
    ],
  },
  {id: 'system', label: '系统', items: [{id: 'settings', label: '系统设置', icon: Settings}]},
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
  settings: {title: '系统设置', subtitle: '账号、认证方式与登录设备'},
}

export const allNavItems: NavItem[] = navGroups.flatMap(g => g.items)
