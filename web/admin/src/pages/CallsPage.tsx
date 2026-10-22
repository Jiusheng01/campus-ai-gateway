import {useCallback} from 'react'
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
import {useResource} from '../lib/useResource'
import {fmt, num} from '../lib/format'

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
 * today's totals. That payload is campus-wide, so a scoped administrator does
 * not ask for it: the gateway would refuse, and the card would describe a set
 * of rows outside their subtree. `ApiKey.usage_requests` is a lifetime counter
 * and must never be substituted for a period figure.
 */
export function CallsPage({scopeTenantID = ''}: {scopeTenantID?: string | null}) {
  const scoped = !!scopeTenantID

  const bundleLoader = useCallback(async (): Promise<CallsBundle> => {
    const [k, t, m] = await Promise.all([
      api<{data: ApiKey[]}>('/admin/api-keys'),
      api<{data: Tenant[]}>('/admin/tenants'),
      api<{data: ModelConfig[]}>('/admin/models'),
    ])
    return {keys: k.data || [], tenants: t.data || [], models: m.data || []}
  }, [])
  const bundle = useResource(bundleLoader, [])

  // Loaded separately so a failing summary cannot blank the key list. A scoped
  // account resolves this to null without a request.
  const status = useResource<DashboardStatus | null>(
    async () => (scoped ? null : api<DashboardStatus>('/admin/status')),
    [scoped],
  )

  const tenants = bundle.data?.tenants || []
  const keys = bundle.data?.keys || []
  const models = bundle.data?.models || []
  const loaded = bundle.data !== null

  const activeTenantsCount = tenants.filter(t => t.status === 1).length
  const activeKeysCount = keys.filter(k => k.status === 1).length
  const enabledModelsCount = models.filter(m => m.status === 1).length

  const reloadAll = () => {
    bundle.reload()
    if (!scoped) status.reload()
  }

  const todayStats = status.data?.stats
  const todayValue = todayStats ? num(todayStats.today_requests) : '—'
  const todaySubtext = scoped
    ? '受限管理员不展示全校统计'
    : todayStats
      ? `今日消耗 ${fmt(todayStats.today_tokens)} Tokens`
      : status.loading
        ? '正在读取今日统计…'
        : (
          <button type="button" className="callsStatUnavailable" onClick={status.reload} title={status.error}>
            今日统计不可用，点击重试
          </button>
        )

  return (
    <div className="stack">
      <PageBanner title="调用管理" subtitle="管理业务调用凭据" />

      <section aria-label="调用管理统计概览">
        <StatGrid columns={scoped ? 3 : 4}>
          <StatCard
            label="租户总数"
            value={loaded ? tenants.length : '—'}
            icon={<Users />}
            subtext={
              bundle.error ? '租户列表读取失败' : loaded ? `正常启用 ${activeTenantsCount} 个租户` : '正在读取租户…'
            }
            tone="default"
          />
          <StatCard
            label="活跃 Key"
            value={loaded ? activeKeysCount : '—'}
            icon={<KeyRound />}
            subtext={
              bundle.error ? 'API Key 列表读取失败' : loaded ? `全部 Key 共 ${keys.length} 个` : '正在读取 API Key…'
            }
            tone="success"
          />
          <StatCard
            label="启用对外模型"
            value={loaded ? enabledModelsCount : '—'}
            icon={<Boxes />}
            subtext={
              bundle.error ? '对外模型列表读取失败' : loaded ? '对外模型中处于启用状态的数量' : '正在读取模型…'
            }
            tone="purple"
          />
          {!scoped && (
            <StatCard
              label="今日调用"
              value={todayValue}
              icon={<Activity />}
              subtext={todaySubtext}
              tone="info"
            />
          )}
        </StatGrid>
      </section>

      <ApiKeysPage
        bundle={bundle.data}
        loading={bundle.loading}
        error={bundle.error}
        errorStatus={bundle.errorStatus}
        onReload={reloadAll}
      />
    </div>
  )
}
