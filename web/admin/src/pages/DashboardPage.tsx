import {useState} from 'react'
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Boxes,
  CircleGauge,
  Database,
  FlaskConical,
  Network,
  Plus,
  RefreshCw,
  Route,
  ServerCog,
  Users,
  Zap,
} from 'lucide-react'
import {
  api,
  fetchTrend,
  listModelTestOptions,
  type DashboardStatus,
  type ModelConfig,
  type ModelTestOption,
  type RequestLog,
  type RouteView,
  type Tenant,
  type TrendBucket,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards, SkeletonRows} from '../components/ui/Skeleton'
import {useToast} from '../components/ui/Toast'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {PublishModelWizard} from '../components/models/PublishModelWizard'
import {ProviderForm, type ProviderDraft} from '../components/providers/ProviderForm'
import {Drawer} from '../components/ui/Drawer'
import {MiniSparkline} from '../components/ui/CampusIllustrations'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {useResource} from '../lib/useResource'
import {ago, fmt, ms, num, ratio, when} from '../lib/format'
import {type Page} from '../app/navigation'

/** The wall-clock day a trend bucket label belongs to ("YYYY-MM-DD HH:00"). */
function localDayKey(at: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}`
}

type TrendDelta = {value: string; isPositive: boolean; neutral: boolean}

/**
 * A signed percentage change against the prior window.
 *
 * A prior value of zero means there is no comparison period to report — the
 * caption is omitted rather than rendered as +∞, 0%, or an invented number.
 */
function percentDelta(prefix: string, current: number, prior: number): TrendDelta | null {
  if (prior <= 0) return null
  const change = ((current - prior) / prior) * 100
  return {
    value: `${prefix} ${change > 0 ? '+' : ''}${change.toFixed(1)}%`,
    isPositive: change > 0,
    neutral: change === 0,
  }
}

function sumOf(buckets: TrendBucket[], key: 'requests' | 'tokens' | 'errors'): number {
  return buckets.reduce((total, bucket) => total + bucket[key], 0)
}

/**
 * Today's error rate against the prior window's, in percentage points.
 *
 * A rate is already a percentage; reporting the relative change of one would
 * read as a percent of a percent. Both windows need requests for a rate to
 * exist, so a prior period without traffic omits the caption.
 */
function errorRateDelta(current: TrendBucket[], prior: TrendBucket[]): TrendDelta | null {
  const currentRequests = sumOf(current, 'requests')
  const priorRequests = sumOf(prior, 'requests')
  if (priorRequests <= 0 || currentRequests <= 0) return null
  const currentRate = currentRequests > 0 ? (sumOf(current, 'errors') / currentRequests) * 100 : 0
  const priorRate = (sumOf(prior, 'errors') / priorRequests) * 100
  const change = currentRate - priorRate
  return {
    value: `本地完整小时较昨日同期 ${change > 0 ? '+' : ''}${change.toFixed(2)} 个百分点`,
    isPositive: change > 0,
    neutral: change === 0,
  }
}

/**
 * The peak bucket P95 in a window.
 *
 * A window P95 cannot be reconstructed from per-bucket P95s, so the worst
 * complete hour is used and the caption says 峰值 rather than claiming a
 * percentile the response does not contain.
 */
function peakP95(buckets: TrendBucket[]): number {
  return buckets.reduce((peak, bucket) => Math.max(peak, bucket.p95_latency_ms), 0)
}

type TrendWindows = {current: TrendBucket[]; prior: TrendBucket[]}

/**
 * Splits one trend response into today and the matching hours of yesterday.
 *
 * The newest bucket is the hour still in progress. Comparing it with a full
 * hour would make every page load look like a fall, so it is dropped — the
 * comparison is complete hours only. The prior window takes the same clock
 * hours at the start of yesterday, and is abandoned entirely when yesterday
 * does not have that many hours yet.
 */
function trendWindows(buckets: TrendBucket[]): TrendWindows | null {
  const todayKey = localDayKey(new Date())
  const todays = buckets.filter(bucket => bucket.bucket.startsWith(todayKey))
  const earlier = buckets.filter(bucket => !bucket.bucket.startsWith(todayKey))
  const current = todays.slice(0, -1)
  const prior = earlier.slice(0, current.length)
  if (current.length === 0 || prior.length !== current.length) return null
  return {current, prior}
}

/**
 * The last 24 hourly points of the same response, on one card's axis.
 *
 * A series with no traffic is omitted rather than drawn flat: zero-height bars
 * are still a shape, and nothing in the data produced one.
 */
function sparklineData(
  buckets: TrendBucket[],
  axis: 'requests' | 'tokens' | 'errors' | 'p95_latency_ms',
): number[] | null {
  const recent = buckets.slice(-24)
  if (recent.length < 2) return null
  const values = recent.map(bucket => bucket[axis])
  return values.some(value => value > 0) ? values : null
}

export function DashboardPage({
  roles,
  onNavigate,
}: {
  roles: string[]
  onNavigate?: (p: Page) => void
}) {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload} = useResource(() => api<DashboardStatus>('/admin/status'))
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)
  const [testLoading, setTestLoading] = useState(false)
  const [publishing, setPublishing] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createBusy, setCreateBusy] = useState(false)
  const [createError, setCreateError] = useState('')

  const canTestModel = roles.includes('super_admin') || roles.includes('gateway_admin') || roles.includes('operator')
  const canReadLogs = roles.includes('super_admin') || roles.includes('auditor')
  const canReadRoutes = roles.includes('super_admin') || roles.includes('gateway_admin')
  const canReadTenants = roles.includes('super_admin')

  // Recent requests query (null normalized to empty array)
  const recent = useResource(() =>
    canReadLogs
      ? api<{data: RequestLog[] | null}>('/admin/request-logs?limit=5').then(d => ({data: d.data || []}))
      : Promise.resolve({data: [] as RequestLog[]}),
  )

  /**
   * The route list feeding the snapshot table.
   *
   * The tile's count does not come from here: `/admin/routes` caps one page at
   * 200 rows (500 with an explicit limit) in the storage layer, so an
   * unparameterised read silently truncates a larger deployment. The models
   * response carries `route_count` per model, and every Route belongs to a
   * model, so that sum is the whole count without a backend change.
   */
  const routes = useResource(() =>
    canReadRoutes
      ? api<{data: RouteView[] | null}>('/admin/routes').then(d => d.data || [])
      : Promise.resolve(null as RouteView[] | null),
  )

  /** The per-model route counts; same role gate as the route collection. */
  const models = useResource(() =>
    canReadRoutes
      ? api<{data: ModelConfig[] | null}>('/admin/models').then(d => d.data || [])
      : Promise.resolve(null as ModelConfig[] | null),
  )

  /** Tenant inventory; only super administrators may read the collection. */
  const tenants = useResource(() =>
    canReadTenants
      ? api<{data: Tenant[] | null}>('/admin/tenants').then(d => d.data || [])
      : Promise.resolve(null as Tenant[] | null),
  )

  /**
   * One trend window feeds all four captions and sparklines.
   *
   * It starts at local midnight yesterday so a single response contains both
   * the comparison period and the last 24 hours. `fetchTrend` applies this
   * browser's UTC offset, so a bucket means the reader's hour, not UTC's.
   */
  const trend = useResource(() => {
    const to = new Date()
    const from = new Date(to)
    from.setHours(0, 0, 0, 0)
    from.setDate(from.getDate() - 1)
    return fetchTrend(from, to, 'hour')
  })

  const openTest = async () => {
    if (testLoading) return
    setTestLoading(true)
    try {
      setTesting(await listModelTestOptions())
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型列表')
    } finally {
      setTestLoading(false)
    }
  }

  const refreshConfig = () => {
    reload()
    routes.reload()
    models.reload()
  }

  const saveProvider = async (value: ProviderDraft) => {
    if (createBusy) return
    setCreateBusy(true)
    setCreateError('')
    try {
      await api('/admin/providers', {method: 'POST', body: JSON.stringify(value)})
      setCreating(false)
      refreshConfig()
      toast.success('渠道已创建，可到渠道详情维护候选模型')
    } catch (e) {
      setCreateError(e instanceof Error ? e.message : '创建渠道失败')
    } finally {
      setCreateBusy(false)
    }
  }

  const stats = data?.stats
  const hasRequests = !!stats && stats.today_requests > 0
  const errorRate = hasRequests ? ratio(stats.today_errors, stats.today_requests) : '—'

  // Only facts /admin/status carries: reachability for the three probed
  // components, and the reported data-plane mode for the fourth. There is no
  // component-latency field, so these cards assert none.
  const health: {
    name: string
    icon: typeof ServerCog
    note: string
    ok?: boolean
    detail?: string
  }[] = [
    {name: 'Gateway', ok: data?.gateway, icon: ServerCog, note: '控制面与数据面'},
    {name: 'PostgreSQL', ok: data?.postgres, icon: Database, note: '配置与日志存储'},
    {name: 'Redis', ok: data?.redis, icon: Boxes, note: '限流与配额计数'},
    {name: '数据面', icon: Route, note: '由网关进程直连上游渠道', detail: data?.data_plane},
  ]

  // Absent counts are unknown, not zero: `0` would claim an empty inventory.
  const counts = data?.config_counts
  // Every model must supply a count; a partial sum is not an inventory total.
  const routeCount =
    models.error || models.data === null
      ? null
      : models.data.every(m => typeof m.route_count === 'number')
        ? models.data.reduce((total, m) => total + (m.route_count ?? 0), 0)
        : null
  const routeSample = routes.data ? routes.data.slice(0, 5) : []
  const enabledRouteCount = !models.error && models.data?.every(m => typeof m.enabled_route_count === 'number')
    ? models.data.reduce((total, m) => total + (m.enabled_route_count ?? 0), 0) : null
  const tenantCount = !tenants.error && tenants.data ? tenants.data.length : null

  const trendPoints = trend.data?.data ?? []
  const windows = trend.data ? trendWindows(trendPoints) : null
  const requestsDelta = windows ? percentDelta('本地完整小时较昨日同期', sumOf(windows.current, 'requests'), sumOf(windows.prior, 'requests')) : null
  const tokensDelta = windows ? percentDelta('本地完整小时较昨日同期', sumOf(windows.current, 'tokens'), sumOf(windows.prior, 'tokens')) : null
  const errorsDelta = windows ? errorRateDelta(windows.current, windows.prior) : null
  const latencyDelta = windows && sumOf(windows.current, 'requests') > 0 ? percentDelta('本地完整小时较昨日同期峰值', peakP95(windows.current), peakP95(windows.prior)) : null

  const requestsSpark = sparklineData(trendPoints, 'requests')
  const tokensSpark = sparklineData(trendPoints, 'tokens')
  const errorsSpark = sparklineData(trendPoints, 'errors')
  const latencySpark = sparklineData(trendPoints, 'p95_latency_ms')

  return (
    <div className="stack overviewPage">
      <PageBanner
        title="服务概览"
        subtitle="查看校园模型服务的请求、配置与依赖状态"
        actions={
          <div className="overviewActions">
            {canReadRoutes && <>
              <Button icon={<Plus />} onClick={() => {setCreateError(''); setCreating(true)}}>新增渠道</Button>
              <Button variant="primary" icon={<Boxes />} onClick={() => setPublishing(true)}>发布模型</Button>
            </>}
            {canTestModel && <Button icon={<FlaskConical />} loading={testLoading} onClick={() => void openTest()}>模型连通测试</Button>}
          </div>
        }
      />

      {error ? (
        <Panel title="运行状态">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : (
        <>
          {/* Four Core Metric Cards; values from status, movement from trend */}
          <section aria-label="核心业务指标" className="overviewKpis">
            <p className="overviewWindowNote">日统计按服务端日界；小图为本地小时序列，环比仅比较完整小时。</p>
            {loading && !data ? (
              <SkeletonCards count={4} />
            ) : (
              <StatGrid columns={4}>
                <StatCard
                  label="今日请求"
                  value={fmt(stats?.today_requests ?? 0)}
                  icon={<Activity />}
                  subtext="业务 API 调用次数"
                  trend={requestsDelta ?? undefined}
                  sparkline={requestsSpark ? <MiniSparkline data={requestsSpark} color="var(--primary)" /> : undefined}
                  tone="default"
                />
                <StatCard
                  label="今日 Tokens"
                  value={stats?.today_tokens ? fmt(stats.today_tokens) : '0'}
                  icon={<Zap />}
                  subtext="输入与输出合计"
                  trend={tokensDelta ?? undefined}
                  sparkline={tokensSpark ? <MiniSparkline data={tokensSpark} color="var(--success)" /> : undefined}
                  tone="success"
                />
                <StatCard
                  label="错误率"
                  value={errorRate}
                  icon={<AlertTriangle />}
                  subtext={hasRequests ? `今日错误 ${num(stats.today_errors)} 次` : '暂无请求'}
                  trend={errorsDelta ?? undefined}
                  sparkline={errorsSpark ? <MiniSparkline data={errorsSpark} color="var(--danger)" /> : undefined}
                  tone={stats && stats.today_errors > 0 ? 'warning' : 'default'}
                />
                <StatCard
                  label="P95 延迟"
                  value={hasRequests ? ms(stats.p95_latency_ms) : '—'}
                  icon={<CircleGauge />}
                  subtext={hasRequests ? `平均 ${ms(stats.avg_latency_ms)}` : '暂无请求'}
                  trend={latencyDelta ?? undefined}
                  sparkline={latencySpark ? <MiniSparkline data={latencySpark} color="var(--accent)" /> : undefined}
                  tone="purple"
                />
              </StatGrid>
            )}
            {trend.error ? <p className="overviewTrendStatus" role="status">小时趋势读取失败：{trend.error} <Button size="sm" onClick={trend.reload}>重试趋势</Button></p>
              : trend.loading ? <p className="overviewTrendStatus" role="status">正在加载小时趋势…</p>
              : !trendPoints.some(point => point.requests > 0) ? <p className="overviewTrendStatus">暂无小时趋势。业务请求产生后将自动显示。</p> : null}
          </section>

          {/* 配置概况 4-Tile Grid Card */}
          <Panel
            title="配置概况"
            sub="配置数量与启用情况，健康状态见下方依赖探测。"
            className="overviewConfigPanel"
          >
            <div className="configTilesGrid">
              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon blue"><Boxes /></span>
                  <span className="configTileName">对外模型</span>
                </div>
                <div className="configTileValue">
                  <b>{counts ? counts.models : '—'}</b> <small>个</small>
                </div>
                <div className="configTileSub">
                  {counts ? `（启用 ${counts.enabled_models} / 总数 ${counts.models}）` : '状态未返回配置数量'}
                </div>
              </div>

              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon green"><Network /></span>
                  <span className="configTileName">上游渠道</span>
                </div>
                <div className="configTileValue">
                  <b>{counts ? counts.providers : '—'}</b> <small>个</small>
                </div>
                <div className="configTileSub">
                  {counts ? `（启用 ${counts.enabled_providers} / 总数 ${counts.providers}）` : '状态未返回配置数量'}
                </div>
              </div>

              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon purple"><Route /></span>
                  <span className="configTileName">模型线路</span>
                </div>
                <div className="configTileValue">
                  <b>{routeCount === null ? '—' : routeCount}</b> <small>条</small>
                </div>
                <div className="configTileSub">
                  {canReadRoutes ? (models.error ? '线路总数读取失败' : routeCount === null ? '线路总数未知' : `启用 ${enabledRouteCount ?? '未知'}，按完整模型计数汇总`) : '当前角色不可读取线路清单'}
                </div>
              </div>

              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon amber"><Users /></span>
                  <span className="configTileName">租户</span>
                </div>
                <div className="configTileValue">
                  <b>{tenantCount === null ? '—' : tenantCount}</b> <small>个</small>
                </div>
                <div className="configTileSub">
                  {canReadTenants
                    ? tenants.error ? '租户列表读取失败' : '配额与 API Key 的归属单位'
                    : '当前角色不可读取租户列表'}
                </div>
              </div>
            </div>
          </Panel>

          {/* 基础设施状态 Card */}
          <Panel
            title="基础设施状态"
            className="overviewHealthPanel"
            actions={
              <div className="panelActionGroup">
                <Button size="sm" icon={<RefreshCw />} loading={loading} onClick={reload}>
                  刷新
                </Button>
              </div>
            }
          >
            <div className="statusRow">
              {health.map(h => {
                const Icon = h.icon
                // A card with no `ok` on the payload (the data plane reports a
                // mode, not health) is unknown, not failed.
                const state = loading && !data ? 'unknown' : h.ok === undefined ? 'unknown' : h.ok ? 'ok' : 'down'
                return (
                  <div className={`statusCard ${h.detail ? 'unknown' : state}`} key={h.name}>
                    <span className="statusIcon" aria-hidden>
                      <Icon />
                    </span>
                    <div className="statusText">
                      <b>{h.name}</b>
                      <small>{h.note}</small>
                    </div>
                    {/* The data-plane card reports its mode; it makes no
                        reachability claim the status response does not carry. */}
                    <span className={`statusChip ${h.detail ? 'unknown' : state === 'ok' ? 'ok' : state === 'down' ? 'down' : 'unknown'}`}>
                      {h.detail ? (
                        `模式：${h.detail}`
                      ) : (
                        <>
                          <i className={`dot ${state === 'ok' ? 'on' : state === 'down' ? 'down' : 'unknown'}`} aria-hidden />
                          {state === 'ok' ? '正常' : state === 'down' ? '异常' : '未知'}
                        </>
                      )}
                    </span>
                  </div>
                )
              })}
            </div>
          </Panel>

          {/* Two Side-by-Side Tables: 路由配置快照 & 最近请求 */}
          <div className="sideBySidePanels">
            {/* Left: 路由配置快照 */}
            <Panel
              title="线路配置"
              sub="展示配置清单前 5 条；运行时缓存状态单独列出。"
              actions={
                canReadRoutes && onNavigate && (
                  <button
                    type="button"
                    className="linkActionBtn"
                    onClick={() => onNavigate('routes')}
                  >
                    查看全部 <ArrowRight aria-hidden />
                  </button>
                )
              }
              className="halfPanel"
            >
              {data?.route_cache?.stale && (
                <p className="overviewCacheWarning" role="status">
                  <AlertTriangle aria-hidden />
                  {`最近重建失败${data.route_cache.last_error_kind ? `（${data.route_cache.last_error_kind}）` : ''}，当前继续使用旧快照；生成时间：${data.route_cache.built_at ? when(data.route_cache.built_at) : '尚未生成'}。`}
                </p>
              )}
              {data?.route_cache && !data.route_cache.stale && (
                <p className="hint">
                  本实例缓存代次 {data.route_cache.generation}，生成时间：
                  {data.route_cache.built_at ? when(data.route_cache.built_at) : '尚未生成'}
                </p>
              )}
              {!data?.route_cache && <p className="hint">本实例缓存状态未返回。</p>}
              {!canReadRoutes ? (
                <div className="cardNotice">当前角色不可读取线路配置。</div>
              ) : routes.error ? (
                <ErrorState message={routes.error} status={routes.errorStatus} onRetry={routes.reload} />
              ) : routes.loading && !routes.data ? (
                <SkeletonRows rows={4} columns={5} />
              ) : routeSample.length ? (
                <Table heads={['#', '对外模型', '上游渠道', '路由策略', '状态']} minWidth={460}>
                  {routeSample.map((r, idx) => (
                    <tr key={r.id}>
                      <td className="muted">{idx + 1}</td>
                      <td>
                        <b className="mono">{r.model_alias}</b>
                      </td>
                      <td>{r.provider_name}</td>
                      <td>
                        <span className="routeModeTag">
                          故障转移层级 {r.priority}
                        </span>
                      </td>
                      <td>
                        <span className={`statusPill ${r.status === 1 ? 'ok' : 'down'}`}>
                          <i className={`dot ${r.status === 1 ? 'on' : 'down'}`} aria-hidden />
                          {r.status === 1 ? '启用' : '停用'}
                        </span>
                      </td>
                    </tr>
                  ))}
                </Table>
              ) : (
                <div className="cardNotice">
                  {data?.route_cache ? (
                    <p className="hint">暂无线路配置，可先发布模型。</p>
                  ) : (
                    <p className="hint">暂无线路配置，可先发布模型。</p>
                  )}
                </div>
              )}
            </Panel>

            {/* Right: 最近请求 */}
            {canReadLogs ? (
              <Panel
                title="最近请求"
                sub="最近 5 条 Request Record"
                actions={
                  <div className="panelActionGroup">
                    <Button size="sm" icon={<RefreshCw />} loading={recent.loading} onClick={recent.reload}>
                      刷新
                    </Button>
                    {onNavigate && (
                      <button
                        type="button"
                        className="linkActionBtn"
                        onClick={() => onNavigate('logs')}
                      >
                        查看全部 <ArrowRight aria-hidden />
                      </button>
                    )}
                  </div>
                }
                className="halfPanel"
              >
                {recent.error ? (
                  <ErrorState message={recent.error} status={recent.errorStatus} onRetry={recent.reload} />
                ) : recent.loading && !recent.data ? (
                  <SkeletonRows rows={4} columns={5} />
                ) : recent.data?.data?.length ? (
                  <Table heads={['时间', '对外模型', '渠道', '状态', '延迟']} minWidth={460}>
                    {recent.data.data.map(r => (
                      <tr key={r.request_id}>
                        <td className="muted">{ago(r.created_at)}</td>
                        <td>
                          <b>{r.requested_model || '—'}</b>
                        </td>
                        <td>{r.provider || '—'}</td>
                        <td>
                          <span className={`badge ${r.status_code >= 400 ? 'danger' : 'success'}`}>
                            {r.status_code < 400 ? '成功' : `失败 (${r.status_code})`}
                          </span>
                        </td>
                        <td className="numeric">{ms(r.latency_ms)}</td>
                      </tr>
                    ))}
                  </Table>
                ) : (
                  <EmptyState
                    title="暂无调用记录"
                    description="业务请求进入网关后，这里会显示最近几条调用。完整检索请到「调用日志」。"
                  />
                )}
              </Panel>
            ) : (
              <Panel title="最近请求" sub="调用记录已向审计与系统管理员开放。" className="halfPanel">
                <p className="hint">当前角色权限不可直接查看原始调用明细。</p>
              </Panel>
            )}
          </div>
        </>
      )}

      {/* Model Test Drawer */}
      {testing && <ModelTestDrawer models={testing} onClose={() => setTesting(null)} />}
      {publishing && <PublishModelWizard onClose={() => setPublishing(false)} onPublished={refreshConfig} />}
      {creating && <Drawer title="新增渠道" subtitle="连接上游服务，之后在渠道详情中维护候选模型。" onClose={() => {if (!createBusy) setCreating(false)}} dismissable={!createBusy}>
        <ProviderForm value={{name: '', base_url: '', protocol: 'openai-chat', provider_type: 'custom', status: 1}} saving={createBusy} error={createError} onSave={value => void saveProvider(value)} onCancel={() => setCreating(false)} />
      </Drawer>}
    </div>
  )
}
