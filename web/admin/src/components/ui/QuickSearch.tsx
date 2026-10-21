import {useEffect, useMemo, useRef, useState} from 'react'
import {
  Boxes,
  FileClock,
  FlaskConical,
  KeyRound,
  LayoutDashboard,
  Network,
  Plus,
  Route,
  Search,
  Settings,
  ShieldAlert,
  ShieldCheck,
  UserRound,
  X,
  type LucideIcon,
} from 'lucide-react'
import {api, type ModelConfig, type Provider} from '../../api'
import {type Page} from '../../app/navigation'

export type QuickSearchItem = {
  id: string
  category: '导航' | '对外模型' | '上游渠道' | '快捷操作'
  title: string
  subtitle?: string
  icon: LucideIcon
  action: () => void
}

type Props = {
  open: boolean
  onClose: () => void
  onNavigate: (page: Page) => void
  onOpenModelTest?: () => void
  roles?: string[]
}

export function QuickSearchModal({open, onClose, onNavigate, onOpenModelTest, roles = []}: Props) {
  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [models, setModels] = useState<ModelConfig[]>([])
  const [providers, setProviders] = useState<Provider[]>([])
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (open) {
      setQuery('')
      setSelectedIndex(0)
      window.setTimeout(() => inputRef.current?.focus(), 50)

      // Fetch models and channels on open for rich search
      if (roles.includes('super_admin') || roles.includes('gateway_admin')) {
        void api<{data: ModelConfig[]}>('/admin/models')
          .then(d => setModels(d.data || []))
          .catch(() => {})
        void api<{data: Provider[]}>('/admin/providers')
          .then(d => setProviders(d.data || []))
          .catch(() => {})
      }
    }
  }, [open, roles])

  const staticNavItems: QuickSearchItem[] = useMemo(() => [
    {
      id: 'nav-dashboard',
      category: '导航',
      title: '服务概览',
      subtitle: '模型服务的运行状态与配置概况',
      icon: LayoutDashboard,
      action: () => { onNavigate('dashboard'); onClose() },
    },
    {
      id: 'nav-providers',
      category: '导航',
      title: '上游渠道',
      subtitle: '管理模型通道的接入、凭据与候选模型',
      icon: Network,
      action: () => { onNavigate('providers'); onClose() },
    },
    {
      id: 'nav-models',
      category: '导航',
      title: '对外模型',
      subtitle: '管理业务系统调用的模型名称与线路配置',
      icon: Boxes,
      action: () => { onNavigate('models'); onClose() },
    },
    {
      id: 'nav-routes',
      category: '导航',
      title: '路由策略',
      subtitle: '查看和编辑所有对外模型的线路',
      icon: Route,
      action: () => { onNavigate('routes'); onClose() },
    },
    {
      id: 'nav-calls',
      category: '导航',
      title: '调用管理',
      subtitle: '管理租户配额与业务调用凭据',
      icon: KeyRound,
      action: () => { onNavigate('calls'); onClose() },
    },
    {
      id: 'nav-logs',
      category: '导航',
      title: '调用日志',
      subtitle: '检索与排查业务请求',
      icon: FileClock,
      action: () => { onNavigate('logs'); onClose() },
    },
    {
      id: 'nav-security',
      category: '导航',
      title: '安全事件',
      subtitle: '鉴权与配额拒绝事件',
      icon: ShieldAlert,
      action: () => { onNavigate('security'); onClose() },
    },
    {
      id: 'nav-users',
      category: '导航',
      title: '用户与权限',
      subtitle: '管理账号与角色',
      icon: UserRound,
      action: () => { onNavigate('users'); onClose() },
    },
    {
      id: 'nav-settings',
      category: '导航',
      title: '系统设置',
      subtitle: '认证方式与运行参数',
      icon: Settings,
      action: () => { onNavigate('settings'); onClose() },
    },
    {
      id: 'nav-access',
      category: '导航',
      title: '安全与访问',
      subtitle: '个人密码与设备管理',
      icon: ShieldCheck,
      action: () => { onNavigate('access'); onClose() },
    },
  ], [onNavigate, onClose])

  const actionItems: QuickSearchItem[] = useMemo(() => [
    {
      id: 'action-test',
      category: '快捷操作',
      title: '模型连通测试',
      subtitle: '测试已发布模型的连通性与延迟',
      icon: FlaskConical,
      action: () => { onClose(); onOpenModelTest?.() },
    },
    {
      id: 'action-new-provider',
      category: '快捷操作',
      title: '新增上游渠道',
      subtitle: '接入新的大模型供应商或自定义网关',
      icon: Plus,
      action: () => { onNavigate('providers'); onClose() },
    },
    {
      id: 'action-new-model',
      category: '快捷操作',
      title: '发布对外模型',
      subtitle: '从渠道候选模型创建对外虚拟模型',
      icon: Boxes,
      action: () => { onNavigate('models'); onClose() },
    },
    {
      id: 'action-new-key',
      category: '快捷操作',
      title: '创建 API 密钥',
      subtitle: '为业务租户颁发访问凭据',
      icon: KeyRound,
      action: () => { onNavigate('calls'); onClose() },
    },
  ], [onClose, onNavigate, onOpenModelTest])

  const modelItems: QuickSearchItem[] = useMemo(() => {
    return models.map(m => ({
      id: `model-${m.id}`,
      category: '对外模型',
      title: m.alias,
      subtitle: `${m.display_name || '未设置显示名'} · ${m.status === 1 ? '已启用' : '已停用'} · 版本 v${m.config_version}`,
      icon: Boxes,
      action: () => { onNavigate('models'); onClose() },
    }))
  }, [models, onNavigate, onClose])

  const providerItems: QuickSearchItem[] = useMemo(() => {
    return providers.map(p => ({
      id: `provider-${p.id}`,
      category: '上游渠道',
      title: p.name,
      subtitle: `${p.provider_type || '自定义'} · ${p.base_url} · ${p.status === 1 ? '在线' : '停用'}`,
      icon: Network,
      action: () => { onNavigate('providers'); onClose() },
    }))
  }, [providers, onNavigate, onClose])

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase()
    const all = [...actionItems, ...staticNavItems, ...modelItems, ...providerItems]
    if (!q) return all.slice(0, 10)
    return all.filter(item =>
      item.title.toLowerCase().includes(q) ||
      (item.subtitle && item.subtitle.toLowerCase().includes(q)) ||
      item.category.toLowerCase().includes(q)
    ).slice(0, 12)
  }, [query, actionItems, staticNavItems, modelItems, providerItems])

  // Reset selected index when filtered list changes
  useEffect(() => {
    setSelectedIndex(0)
  }, [filteredItems.length])

  // Keyboard navigation
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
      } else if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedIndex(prev => (prev + 1) % Math.max(1, filteredItems.length))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedIndex(prev => (prev - 1 + filteredItems.length) % Math.max(1, filteredItems.length))
      } else if (e.key === 'Enter') {
        e.preventDefault()
        if (filteredItems[selectedIndex]) {
          filteredItems[selectedIndex].action()
        }
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose, filteredItems, selectedIndex])

  if (!open) return null

  return (
    <div className="overlay center quickSearchOverlay" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="quickSearchModal" role="dialog" aria-modal="true" aria-label="全局快速搜索">
        <div className="quickSearchInputBar">
          <Search className="quickSearchIcon" aria-hidden />
          <input
            ref={inputRef}
            type="text"
            className="quickSearchInput"
            placeholder="搜索渠道、模型、路由或 API Key..."
            value={query}
            onChange={e => setQuery(e.target.value)}
          />
          {query && (
            <button type="button" className="quickSearchClear" onClick={() => setQuery('')} aria-label="清空输入">
              <X aria-hidden />
            </button>
          )}
          <kbd className="quickSearchKbd">ESC</kbd>
        </div>

        <div className="quickSearchResults">
          {filteredItems.length ? (
            filteredItems.map((item, idx) => {
              const Icon = item.icon
              const isSelected = idx === selectedIndex
              return (
                <div
                  key={item.id}
                  className={`quickSearchItem ${isSelected ? 'selected' : ''}`}
                  onClick={item.action}
                  onMouseEnter={() => setSelectedIndex(idx)}
                >
                  <span className="quickSearchItemIcon" aria-hidden>
                    <Icon />
                  </span>
                  <div className="quickSearchItemText">
                    <div className="quickSearchItemHead">
                      <b className="quickSearchItemTitle">{item.title}</b>
                      <span className="quickSearchItemCategory">{item.category}</span>
                    </div>
                    {item.subtitle && <p className="quickSearchItemSub">{item.subtitle}</p>}
                  </div>
                </div>
              )
            })
          ) : (
            <div className="quickSearchEmpty">
              未找到与「{query}」相关的页面、模型或渠道
            </div>
          )}
        </div>

        <footer className="quickSearchFoot">
          <span><kbd>↑</kbd> <kbd>↓</kbd> 切换选项</span>
          <span><kbd>Enter</kbd> 确认跳转</span>
          <span><kbd>ESC</kbd> 关闭搜索</span>
        </footer>
      </div>
    </div>
  )
}
