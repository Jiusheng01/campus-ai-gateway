import {useCallback, useEffect, useMemo, useState} from 'react'
import {ArrowLeftRight, Layers, Link2, Pencil, Route, Sparkles} from 'lucide-react'
// The workspace is addressed by pathname, so an entity link is a real anchor:
// the target page resolves the address on load, and the browser keeps its
// own back behaviour. A router call would have to reproduce both.
const Link = ({href, className, title, children}: {href: string; className?: string; title?: string; children: React.ReactNode}) => (
  <a href={href} className={className} title={title}>{children}</a>
)
import {api, type RouteView} from '../api'
import {Button} from '../components/ui/Button'
import {Badge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Modal} from '../components/ui/Modal'
import {RouteEditor} from '../components/models/RouteEditor'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {useResource} from '../lib/useResource'
import {pathFor} from '../app/routing'
import {ms} from '../lib/format'

type Filters = {model: string; provider: string; status: string}

type LineShape = 'single' | 'failover' | 'weighted' | 'mixed'

type ModelRouteGroup = {
  model_id: string
  model_alias: string
  model_display_name: string
  config_version: number
  routes: RouteView[]
  shape: LineShape
  shapeLabel: string
  shapeDesc: string
  /** At least one line is enabled and points at an enabled channel. */
  servesTraffic: boolean
  /** Enabled lines whose channel is also enabled; what actually serves traffic. */
  liveCount: number
}

/**
 * The shape of a model's line set, described by what the router does with it.
 *
 * The classification is deliberately about configuration, not about a chosen
 * "strategy": priority still dominates, and weight only orders lines inside one
 * priority band. Equal priorities with weights are therefore a first-choice
 * split; increasing priorities with equal weights are a failover chain; both at
 * once is a chain where each layer can split.
 */
function analyzeShape(routes: RouteView[]): {shape: LineShape; shapeLabel: string; shapeDesc: string} {
  const enabled = routes.filter(r => r.status === 1)
  const priorities = new Set(enabled.map(r => r.priority))
  const equalPriorities = enabled.length > 1 && priorities.size === 1
  const allDistinct = priorities.size === enabled.length

  if (enabled.length <= 1) {
    return {
      shape: 'single',
      shapeLabel: '单线路',
      shapeDesc: enabled.length === 0 ? '没有启用线路，该模型不会接到请求。' : '只有一条启用线路，没有同层分流或备用层级。',
    }
  }
  if (equalPriorities) {
    return {
      shape: 'weighted',
      shapeLabel: '同层分流',
      shapeDesc: '启用线路同一优先级，按权重决定由谁开始一次请求；其余线路按权重顺序接替。',
    }
  }
  if (allDistinct) {
    return {
      shape: 'failover',
      shapeLabel: '主备层级',
      shapeDesc: '启用线路各占一个优先级，前一层不可用时进入下一层。',
    }
  }
  return {
    shape: 'mixed',
    shapeLabel: '层级加同层分流',
    shapeDesc: '优先级决定层级顺序，同层内按权重决定由谁开始；每一层都可能有多条线路。',
  }
}

/** Filters live in the address so a link back from a channel or model lands on the same view. */
function filtersFromAddress(): Filters {
  const q = new URLSearchParams(window.location.search)
  return {model: q.get('model') || '', provider: q.get('provider') || '', status: q.get('status') || ''}
}

export function RoutesPage() {
  const [draft, setDraft] = useState<Filters>(filtersFromAddress)
  const [filters, setFilters] = useState<Filters>(filtersFromAddress)

  // The applied filters are the address. A channel or model page links back
  // with the filter already set, and a reload keeps the view the operator was
  // reading instead of silently widening it to every model.
  useEffect(() => {
    const q = new URLSearchParams()
    if (filters.model) q.set('model', filters.model)
    if (filters.provider) q.set('provider', filters.provider)
    if (filters.status) q.set('status', filters.status)
    const search = q.toString()
    const target = window.location.pathname + (search ? `?${search}` : '')
    if (window.location.pathname !== '/admin-ui/routes' || window.location.search !== (search ? `?${search}` : '')) {
      window.history.replaceState(window.history.state, '', target)
    }
  }, [filters])

  // A link back from the channel page is a full page load, but an in-app
  // navigation is a pushState: follow both so the form reflects the address.
  useEffect(() => {
    const onPop = () => {
      const next = filtersFromAddress()
      setDraft(next)
      setFilters(next)
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

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
      const {shape, shapeLabel, shapeDesc} = analyzeShape(modelRoutes)
      const liveCount = modelRoutes.filter(r => r.status === 1 && r.provider_status === 1).length
      result.push({
        model_id: modelId,
        model_alias: first?.model_alias || modelId,
        model_display_name: first?.model_display_name || '',
        config_version: first?.config_version || 1,
        routes: modelRoutes.sort((a, b) => a.priority - b.priority),
        shape,
        shapeLabel,
        shapeDesc,
        servesTraffic: liveCount > 0,
        liveCount,
      })
    })

    return result
  }, [routes])

  const singleCount = groupedModels.filter(m => m.shape === 'single').length
  const failoverCount = groupedModels.filter(m => m.shape === 'failover').length
  const splitCount = groupedModels.filter(m => m.shape === 'weighted' || m.shape === 'mixed').length
  const totalCount = routes.length
  const filtersActive = Object.values(filters).some(v => v.trim() !== '')

  return (
    <div className="stack">
      <PageBanner
        title="路由策略"
        subtitle="为对外模型配置线路。优先级越小越先尝试，同层按权重分流。"
      />

      <section aria-label="线路概况">
        <StatGrid columns={4}>
          <StatCard
            label="单线路"
            value={singleCount}
            icon={<Route />}
            subtext="只有一个启用层级，没有备用"
            tone="default"
          />
          <StatCard
            label="主备层级"
            value={failoverCount}
            icon={<ArrowLeftRight />}
            subtext="按优先级逐层接替"
            tone="success"
          />
          <StatCard
            label="同层分流"
            value={splitCount}
            icon={<Layers />}
            subtext="同优先级内按权重分配，含多层混合"
            tone="purple"
          />
          <StatCard
            label="线路总数"
            value={totalCount}
            icon={<Link2 />}
            subtext={`${filtersActive ? '当前筛选：' : ''}共覆盖 ${groupedModels.length} 个对外模型`}
            tone="info"
          />
        </StatGrid>
      </section>

      <div className="filterCard">
        <div className="filtersBar">
          <label className="filterFieldInline">
            <span className="filterLabel">对外模型</span>
            <input
              className="filterInput"
              placeholder="请输入对外 alias"
              value={draft.model}
              onChange={e => setDraft({...draft, model: e.target.value})}
              onKeyDown={e => e.key === 'Enter' && setFilters(draft)}
            />
          </label>

          <label className="filterFieldInline">
            <span className="filterLabel">渠道</span>
            <input
              className="filterInput"
              placeholder="请输入渠道名称"
              value={draft.provider}
              onChange={e => setDraft({...draft, provider: e.target.value})}
              onKeyDown={e => e.key === 'Enter' && setFilters(draft)}
            />
          </label>

          <label className="filterFieldInline">
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
                <header className="routeModelGroupHead">
                  <div className="routeModelHeadLeft">
                    <div className="routeModelIconMark">
                      <Sparkles aria-hidden />
                    </div>
                    <div className="routeModelTitleBlock">
                      <div className="routeModelNameRow">
                        <Link
                          className="mono routeModelAlias"
                          href={`${pathFor('admin', 'models')}?model=${encodeURIComponent(gm.model_id)}`}
                          title={`在对外模型中查看 ${gm.model_alias}`}
                        >
                          {gm.model_alias}
                        </Link>
                        {gm.model_display_name && (
                          <span className="routeModelDisplayName">{gm.model_display_name}</span>
                        )}
                        {/* A model serves traffic only when at least one of its
                            lines is enabled and its channel is enabled too. */}
                        <span className={`statusChip ${gm.servesTraffic ? 'ok' : 'down'}`}>
                          <i className={`dot ${gm.servesTraffic ? 'on' : 'down'}`} aria-hidden />
                          {gm.servesTraffic ? '已启用' : '无可用线路'}
                        </span>
                      </div>
                      <div className="routeModelSubInfo">
                        <span>版本 v{gm.config_version}</span>
                        <span className="routeModelDot">·</span>
                        <span className="routeModelLinesCount">
                          {gm.liveCount} / {gm.routes.length} 条线路可承载请求
                        </span>
                      </div>
                    </div>
                  </div>

                  <div className="routeModelHeadRight">
                    <div className="routeModeBadgeBlock">
                      <span className={`routeStrategyPill ${gm.shape}`}>
                        {gm.shapeLabel}
                      </span>
                      <span className="routeStrategyDesc">{gm.shapeDesc}</span>
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
                          {/* Channel names are not unique, so the link carries
                              the id; the channel page narrows its own list. */}
                          <Link
                            className="mono tableEntityLink"
                            href={`${pathFor('admin', 'providers')}?provider=${encodeURIComponent(r.provider_id)}`}
                            title={`在上游渠道中查看 ${r.provider_name}`}
                          >
                            {r.provider_name}
                          </Link>
                        </td>
                        <td>
                          <span className="mono">{r.upstream_model}</span>
                        </td>
                        <td>
                          <span className="priorityTag">
                            {r.priority === 1 ? '第 1 层' : `第 ${r.priority} 层`}
                          </span>
                        </td>
                        <td className="numeric">
                          <span className="weightTag">{r.weight === 0 ? '按 1 计' : r.weight}</span>
                        </td>
                        <td className="numeric">{ms(r.timeout_ms)}</td>
                        <td>
                          <span className={`statusChip ${r.status === 1 ? 'ok' : 'down'}`}>
                            <i className={`dot ${r.status === 1 ? 'on' : 'down'}`} aria-hidden />
                            {r.status === 1 ? '启用' : '停用'}
                          </span>
                          {r.provider_status !== 1 && (
                            <Badge tone="warning">渠道停用</Badge>
                          )}
                        </td>
                        <td>
                          {/* Configured is not healthy: the last diagnostic may
                              have failed, and this column is not a probe. */}
                          {r.credential_configured ? (
                            <span className="statusChip ok">
                              <i className="dot on" aria-hidden />
                              已配置
                            </span>
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
