import {useCallback, useMemo, useState} from 'react'
import {
  Activity,
  Boxes,
  KeyRound,
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

/** The three resources the key table and its drawer need. Loaded once, here. */
type CallsBundle = {
  keys: ApiKey[]
  tenants: Tenant[]
  models: ModelConfig[]
}

/**
 * 调用管理 (Call Management)
 *
 * The period figures come from `/admin/status`, the one endpoint that reports
 * today's totals. `ApiKey.usage_requests` is a lifetime counter; it belongs on
 * the key's own row (where "累计" is what it says) and must never be substituted
 * for a number under a period label.
 */
export function CallsPage({initialTab = 'keys'}: {initialTab?: Tab}) {
  const [tab, setTab] = useState<Tab>(initialTab)

  const bundleLoader = useCallback(async (): Promise<CallsBundle> => {
    const [k, t, m] = await Promise.all([
      api<{data: ApiKey[]}>('/admin/api-keys'),
      api<{data: Tenant[]}>('/admin/tenants'),
      api<{data: ModelConfig[]}>('/admin/models'),
    ])
    return {keys: k.data || [], tenants: t.data || [], models: m.data || []}
  }, [])
  const bundle = useResource(bundleLoader, [])

  // Loaded separately so a failing summary cannot blank the key and tenant
  // lists, and a failing list cannot hide today's figures.
  const status = useResource(() => api<DashboardStatus>('/admin/status'))

  const tenants = bundle.data?.tenants || []
  const keys = bundle.data?.keys || []
  const models = bundle.data?.models || []
  // The bundle resolves to `null` only before the first response; a post-load
  // reload keeps the previous data on screen, so cards never flash a dash.
  const loaded = bundle.data !== null

  const tenantCount = tenants.length
  const activeTenantsCount = tenants.filter(t => t.status === 1).length
  const activeKeysCount = keys.filter(k => k.status === 1).length
  const totalKeysCount = keys.length

  // Enabled Virtual Models, straight from /admin/models. The previous card
  // counted distinct aliases across key allow-lists and fell back to every
  // enabled model when no key named any — two different quantities under one
  // label. This is one quantity the endpoint can answer by itself.
  const enabledModelsCount = models.filter(m => m.status === 1).length

  // The tenant tab is handed the count of keys it would delete with a tenant,
  // because the delete confirmation has to name the cost and this page already
  // holds the keys it would otherwise have to fetch again.
  const keyCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const k of keys) counts[k.tenant_id] = (counts[k.tenant_id] || 0) + 1
    return counts
  }, [keys])

  const reloadAll = () => {
    bundle.reload()
    status.reload()
  }

  // `stats` is present only on a successful response; on failure the card says
  // so instead of showing a number from anywhere else. The previous fallback
  // summed `ApiKey.usage_requests`, a lifetime counter, and labelled the sum
  // with today's period — an error is more honest than that substitution.
  const todayStats = status.data?.stats
  const todayValue = todayStats ? num(todayStats.today_requests) : '—'
  const todaySubtext = todayStats ? (
    `今日消耗 ${fmt(todayStats.today_tokens)} Tokens`
  ) : status.loading ? (
    '正在读取今日统计…'
  ) : (
    <button type="button" className="callsStatUnavailable" onClick={status.reload} title={status.error}>
      今日统计不可用，点击重试
    </button>
  )

  return (
    <div className="stack">
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
              API Key
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

      <section aria-label="调用管理统计概览">
        <StatGrid columns={4}>
          <StatCard
            label="租户总数"
            value={loaded ? tenantCount : '—'}
            icon={<Users />}
            subtext={
              bundle.error
                ? '租户列表读取失败'
                : loaded
                  ? `正常启用 ${activeTenantsCount} 个租户`
                  : '正在读取租户…'
            }
            tone="default"
          />
          <StatCard
            label="活跃 Key"
            value={loaded ? activeKeysCount : '—'}
            icon={<KeyRound />}
            subtext={
              bundle.error
                ? 'API Key 列表读取失败'
                : loaded
                  ? `全部 Key 共 ${totalKeysCount} 个`
                  : '正在读取 API Key…'
            }
            tone="success"
          />
          <StatCard
            label="启用对外模型"
            value={loaded ? enabledModelsCount : '—'}
            icon={<Boxes />}
            subtext={
              bundle.error
                ? '对外模型列表读取失败'
                : loaded
                  ? '对外模型中处于启用状态的数量'
                  : '正在读取模型…'
            }
            tone="purple"
          />
          <StatCard
            label="今日调用"
            value={todayValue}
            icon={<Activity />}
            subtext={todaySubtext}
            tone="info"
          />
        </StatGrid>
      </section>

      {tab === 'keys' ? (
        <ApiKeysPage
          bundle={bundle.data}
          loading={bundle.loading}
          error={bundle.error}
          errorStatus={bundle.errorStatus}
          onReload={reloadAll}
        />
      ) : (
        <TenantsPage
          tenants={bundle.data ? tenants : null}
          models={bundle.data?.models ?? []}
          keyCounts={keyCounts}
          loading={bundle.loading}
          error={bundle.error}
          errorStatus={bundle.errorStatus}
          onReload={reloadAll}
        />
      )}
    </div>
  )
}
