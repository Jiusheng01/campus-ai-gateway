import {useCallback, useMemo, useState} from 'react'
import {
  Activity,
  AlertCircle,
  Boxes,
  CheckCircle2,
  Clock,
  Layers,
  Network,
  Pencil,
  Power,
  RefreshCw,
  Route,
  Search,
  Shield,
  Sliders,
  Sparkles,
  Zap,
} from 'lucide-react'
import {api, type RouteView} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Modal} from '../components/ui/Modal'
import {RouteEditor} from '../components/models/RouteEditor'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {useResource} from '../lib/useResource'
import {ms} from '../lib/format'

type Filters = {model: string; provider: string; status: string}

type ModelRouteGroup = {
  model_id: string
  model_alias: string
  model_display_name: string
  config_version: number
  routes: RouteView[]
  mode: 'primary' | 'weighted' | 'advanced'
  modeLabel: string
  modeDesc: string
  isOnline: boolean
}

function analyzeRouteMode(routes: RouteView[]): {
  mode: 'primary' | 'weighted' | 'advanced'
  modeLabel: string
  modeDesc: string
} {
  if (routes.length <= 1) {
    return {
      mode: 'primary',
      modeLabel: '主备',
      modeDesc: '按优先级依次尝试，主线路不可用时自动切换到备线路',
    }
  }

  // If all active routes have same priority, it's weighted
  const activeRoutes = routes.filter(r => r.status === 1)
  const priorities = new Set(activeRoutes.map(r => r.priority))
  if (priorities.size === 1 && activeRoutes.length > 1) {
    return {
      mode: 'weighted',
      modeLabel: '加权',
      modeDesc: '按权重比例分配请求流量，适用于多渠道负载均衡',
    }
  }

  // If distinct priorities
  const allDistinct = priorities.size === activeRoutes.length
  if (allDistinct) {
    return {
      mode: 'primary',
      modeLabel: '主备',
      modeDesc: '按优先级依次尝试，主线路不可用时自动切换到备线路',
    }
  }

  return {
    mode: 'advanced',
    modeLabel: '高级',
    modeDesc: '支持同层加权与跨优先级故障切换策略',
  }
}

export function RoutesPage() {
  const [draft, setDraft] = useState<Filters>({model: '', provider: '', status: ''})
  const [filters, setFilters] = useState<Filters>({model: '', provider: '', status: ''})

  const loader = useCallback(async () => {
    const q = new URLSearchParams()
    if (filters.model.trim()) q.set('model', filters.model.trim())
    if (filters.provider.trim()) q.set('provider', filters.provider.trim())
    if (filters.status) q.set('status', filters.status)
    const d = await api<{data: RouteView[]}>(`/admin/routes?${q}`)
    return d.data || []
  }, [filters])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [filters])
  const [editing, setEditing] = useState<{id: string; alias: string} | null>(null)
  const routes = data || []

  // Group routes by model
  const groupedModels: ModelRouteGroup[] = useMemo(() => {
    const map = new Map<string, RouteView[]>()
    routes.forEach(r => {
      const list = map.get(r.model_id) || []
      list.push(r)
      map.set(r.model_id, list)
    })

    const result: ModelRouteGroup[] = []
    map.forEach((modelRoutes, modelId) => {
      const first = modelRoutes[0]
      const {mode, modeLabel, modeDesc} = analyzeRouteMode(modelRoutes)
      const isOnline = modelRoutes.some(r => r.status === 1 && r.provider_status === 1)
      result.push({
        model_id: modelId,
        model_alias: first?.model_alias || modelId,
        model_display_name: first?.model_display_name || '',
        config_version: first?.config_version || 1,
        routes: modelRoutes.sort((a, b) => a.priority - b.priority),
        mode,
        modeLabel,
        modeDesc,
        isOnline,
      })
    })

    return result
  }, [routes])

  // Count strategy modes
  const primaryCount = groupedModels.filter(m => m.mode === 'primary').length
  const weightedCount = groupedModels.filter(m => m.mode === 'weighted').length
  const advancedCount = groupedModels.filter(m => m.mode === 'advanced').length
  const totalCount = routes.length

  return (
    <div className="stack">
      {/* Top Banner with Campus Illustration */}
      <PageBanner
        title="路由策略"
        subtitle="查看和编辑所有对外模型的线路。优先级数字越小越先尝试；同层权重只表示配置目标，不代表实际成功率。"
      />

      {/* 4 Strategy Summary Cards matching image 4 */}
      <section aria-label="策略概览统计">
        <StatGrid columns={4}>
          <StatCard
            label="主备策略"
            value={primaryCount}
            icon={<Shield />}
            subtext="优先级主备切换，稳定可靠"
            tone="default"
          />
          <StatCard
            label="加权策略"
            value={weightedCount}
            icon={<Layers />}
            subtext="按权重分配流量，负载均衡"
            tone="success"
          />
          <StatCard
            label="高级策略"
            value={advancedCount}
            icon={<Sliders />}
            subtext="同层加权与多层回退策略"
            tone="purple"
          />
          <StatCard
            label="生效线路总数"
            value={totalCount}
            icon={<Route />}
            subtext={`共覆盖 ${groupedModels.length} 个对外模型`}
            tone="info"
          />
        </StatGrid>
      </section>

      {/* Filter Row matching image 4 */}
      <div className="filterCard">
        <div className="filtersBar">
          <label className="filterField">
            <span className="filterLabel">对外模型</span>
            <input
              className="filterInput"
              placeholder="请输入对外 alias"
              value={draft.model}
              onChange={e => setDraft({...draft, model: e.target.value})}
              onKeyDown={e => e.key === 'Enter' && setFilters(draft)}
            />
          </label>

          <label className="filterField">
            <span className="filterLabel">渠道</span>
            <input
              className="filterInput"
              placeholder="请输入渠道名称"
              value={draft.provider}
              onChange={e => setDraft({...draft, provider: e.target.value})}
              onKeyDown={e => e.key === 'Enter' && setFilters(draft)}
            />
          </label>

          <label className="filterField">
            <span className="filterLabel">状态</span>
            <select
              className="filterSelect"
              value={draft.status}
              onChange={e => setDraft({...draft, status: e.target.value})}
            >
              <option value="">全部</option>
              <option value="1">启用</option>
              <option value="2">停用</option>
            </select>
          </label>

          <div className="filterActionButtons">
            <Button
              onClick={() => {
                const empty = {model: '', provider: '', status: ''}
                setDraft(empty)
                setFilters(empty)
              }}
            >
              重置
            </Button>
            <Button variant="primary" onClick={() => setFilters(draft)}>
              筛选
            </Button>
          </div>
        </div>
      </div>

      {/* Grouped Routing List matching image 4 */}
      <section aria-label="路由配置列表" className="stack">
        <div className="sectionHeaderWithCount">
          <h2 className="sectionTitle">
            路由配置列表 <small className="sectionCount">（共 {groupedModels.length} 个对外模型）</small>
          </h2>
        </div>

        {loading && !data ? (
          <SkeletonRows rows={6} columns={8} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : groupedModels.length ? (
          <div className="routeModelCardsStack">
            {groupedModels.map(gm => (
              <article className="routeModelGroupCard" key={gm.model_id}>
                {/* Model Header in the Card matching image 4 */}
                <header className="routeModelGroupHead">
                  <div className="routeModelHeadLeft">
                    <div className="routeModelIconMark">
                      <Sparkles aria-hidden />
                    </div>
                    <div className="routeModelTitleBlock">
                      <div className="routeModelNameRow">
                        <b className="routeModelAlias mono">{gm.model_alias}</b>
                        {gm.model_display_name && (
                          <span className="routeModelDisplayName">{gm.model_display_name}</span>
                        )}
                        <span className={`statusPill ${gm.isOnline ? 'ok' : 'down'}`}>
                          <i className={`dot ${gm.isOnline ? 'on' : 'down'}`} aria-hidden />
                          {gm.isOnline ? '已启用' : '已停用'}
                        </span>
                      </div>
                      <div className="routeModelSubInfo">
                        <span>版本 v{gm.config_version}</span>
                        <span className="routeModelDot">·</span>
                        <span className="routeModelLinesCount">{gm.routes.length} 条线路</span>
                      </div>
                    </div>
                  </div>

                  <div className="routeModelHeadRight">
                    <div className="routeModeBadgeBlock">
                      <span className={`routeStrategyPill ${gm.mode}`}>
                        {gm.modeLabel}策略
                      </span>
                      <span className="routeStrategyDesc">{gm.modeDesc}</span>
                    </div>

                    <Button
                      size="sm"
                      variant="primary"
                      icon={<Pencil />}
                      onClick={() => setEditing({id: gm.model_id, alias: gm.model_alias})}
                    >
                      编辑线路
                    </Button>
                  </div>
                </header>

                {/* Nested Routes Table matching image 4 */}
                <div className="routeModelTableWrap">
                  <Table
                    heads={[
                      '#',
                      '渠道',
                      '上游模型',
                      '优先级',
                      '权重',
                      '超时时间',
                      '状态',
                      '凭据状态',
                      '操作',
                    ]}
                    minWidth={880}
                  >
                    {gm.routes.map((r, idx) => (
                      <tr key={r.id}>
                        <td className="muted mono">#{idx + 1}</td>
                        <td>
                          <b className="mono">{r.provider_name}</b>
                        </td>
                        <td>
                          <span className="mono">{r.upstream_model}</span>
                        </td>
                        <td>
                          <span className="priorityTag">
                            {r.priority === 1 ? '主 (1)' : `备 (${r.priority})`}
                          </span>
                        </td>
                        <td className="numeric">
                          {gm.mode === 'weighted' ? (
                            <span className="weightTag">{r.weight}%</span>
                          ) : (
                            <span className="muted">{r.weight === 0 ? '默认' : r.weight}</span>
                          )}
                        </td>
                        <td className="numeric">{ms(r.timeout_ms)}</td>
                        <td>
                          <span className={`statusPill ${r.status === 1 ? 'ok' : 'down'}`}>
                            <i className={`dot ${r.status === 1 ? 'on' : 'down'}`} aria-hidden />
                            {r.status === 1 ? '启用' : '停用'}
                          </span>
                          {r.provider_status !== 1 && (
                            <Badge tone="warning">渠道停用</Badge>
                          )}
                        </td>
                        <td>
                          {r.credential_configured ? (
                            <span className="textSuccess">✓ 正常</span>
                          ) : (
                            <Badge tone="warning">未配置凭据</Badge>
                          )}
                        </td>
                        <td>
                          <div className="rowActions">
                            <button
                              type="button"
                              className="tableActionLink"
                              onClick={() => setEditing({id: gm.model_id, alias: gm.model_alias})}
                            >
                              编辑
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </Table>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <EmptyState
            icon={<Route />}
            title="暂无线路"
            description="发布对外模型后，这里会显示它的全部线路与主备调度配置。"
          />
        )}
      </section>

      {/* Editing Route Modal */}
      {editing && (
        <Modal
          title={`编辑线路：${editing.alias}`}
          subtitle="整组保存，带配置版本校验与实时 diff 检查。"
          onClose={() => setEditing(null)}
        >
          <RouteEditor
            modelID={editing.id}
            onSaved={() => {
              reload()
            }}
          />
        </Modal>
      )}
    </div>
  )
}
