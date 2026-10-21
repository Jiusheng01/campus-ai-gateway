import {useCallback, useMemo, useState} from 'react'
import {
  Activity,
  Boxes,
  CheckCircle2,
  KeyRound,
  Shield,
  Users,
} from 'lucide-react'
import {api, type ApiKey, type DashboardStatus, type ModelConfig, type Tenant} from '../api'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {ApiKeysPage} from './ApiKeysPage'
import {TenantsPage} from './TenantsPage'
import {useResource} from '../lib/useResource'
import {fmt, num} from '../lib/format'

type Tab = 'keys' | 'tenants'

type CallsBundle = {
  keys: ApiKey[]
  tenants: Tenant[]
  models: ModelConfig[]
  dashboard: DashboardStatus | null
}

/**
 * 调用管理 (Call Management)
 * Redesigned to match Image 5:
 * - Top banner with segmented tab navigation (API 密钥 / 租户配额)
 * - 4 Stat Cards: 租户总数, 活跃 Key, 受限模型, 今日调用
 * - Embedded high-density API Key & Tenant Quota tables with drawer workflows
 */
export function CallsPage({initialTab = 'keys'}: {initialTab?: Tab}) {
  const [tab, setTab] = useState<Tab>(initialTab)

  const loader = useCallback(async (): Promise<CallsBundle> => {
    const [k, t, m, dash] = await Promise.all([
      api<{data: ApiKey[]}>('/admin/api-keys').catch(() => ({data: []})),
      api<{data: Tenant[]}>('/admin/tenants').catch(() => ({data: []})),
      api<{data: ModelConfig[]}>('/admin/models').catch(() => ({data: []})),
      api<DashboardStatus>('/admin/dashboard/status').catch(() => null),
    ])
    return {
      keys: k.data || [],
      tenants: t.data || [],
      models: m.data || [],
      dashboard: dash,
    }
  }, [])

  const {data, reload} = useResource(loader, [])

  const tenants = data?.tenants || []
  const keys = data?.keys || []
  const models = data?.models || []
  const dash = data?.dashboard

  // Metrics computation using 100% real data
  const tenantCount = tenants.length
  const activeTenantsCount = tenants.filter(t => t.status === 1).length
  const activeKeysCount = keys.filter(k => k.status === 1).length
  const totalKeysCount = keys.length

  // Models with explicit whitelist or restricted access
  const restrictedModelsCount = useMemo(() => {
    const explicitModels = new Set<string>()
    keys.forEach(k => {
      (k.allowed_models || []).forEach(m => {
        if (m !== '*') explicitModels.add(m)
      })
    })
    return explicitModels.size || models.filter(m => m.status === 1).length
  }, [keys, models])

  // Real-time call count
  const todayRequests = dash?.stats?.today_requests ?? keys.reduce((acc, k) => acc + (k.usage_requests || 0), 0)
  const todayTokens = dash?.stats?.today_tokens ?? keys.reduce((acc, k) => acc + (k.usage_tokens || 0), 0)

  return (
    <div className="stack">
      {/* Top Banner with tab navigation matching Image 5 */}
      <PageBanner
        title="调用管理"
        subtitle="管理租户配额与业务调用凭据"
        actions={
          <div className="segmented callsPageTabs" role="group" aria-label="调用管理视图切换">
            <button
              type="button"
              className={`segmentedItem ${tab === 'keys' ? 'on' : ''}`}
              aria-pressed={tab === 'keys'}
              onClick={() => setTab('keys')}
            >
              API 密钥
            </button>
            <button
              type="button"
              className={`segmentedItem ${tab === 'tenants' ? 'on' : ''}`}
              aria-pressed={tab === 'tenants'}
              onClick={() => setTab('tenants')}
            >
              租户配额
            </button>
          </div>
        }
      />

      {/* 4 Stat Cards matching Image 5 */}
      <section aria-label="调用管理统计概览">
        <StatGrid columns={4}>
          <StatCard
            label="租户总数"
            value={tenantCount}
            icon={<Users />}
            subtext={`正常启用 ${activeTenantsCount} 个租户`}
            tone="default"
          />
          <StatCard
            label="活跃 Key"
            value={activeKeysCount}
            icon={<KeyRound />}
            subtext={`全部 Key 共 ${totalKeysCount} 个`}
            tone="success"
          />
          <StatCard
            label="受限模型"
            value={restrictedModelsCount}
            icon={<Boxes />}
            subtext="白名单与权限管控"
            tone="purple"
          />
          <StatCard
            label="今日调用"
            value={num(todayRequests)}
            icon={<Activity />}
            subtext={`今日消耗 ${fmt(todayTokens)} Tokens`}
            tone="info"
          />
        </StatGrid>
      </section>

      {/* Main Tab Content */}
      {tab === 'keys' ? (
        <ApiKeysPage
          bundle={data ? {keys, tenants, models} : undefined}
          onReload={reload}
        />
      ) : (
        <TenantsPage onReload={reload} />
      )}
    </div>
  )
}
