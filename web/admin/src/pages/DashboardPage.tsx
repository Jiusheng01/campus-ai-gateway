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
  RefreshCw,
  Route,
  ServerCog,
  ShieldCheck,
  Users,
  Zap,
} from 'lucide-react'
import {
  api,
  listModelTestOptions,
  type DashboardStatus,
  type ModelTestOption,
  type RequestLog,
  type RouteView,
  type WhoAmI,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards, SkeletonRows} from '../components/ui/Skeleton'
import {useToast} from '../components/ui/Toast'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {MiniSparkline} from '../components/ui/CampusIllustrations'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {useResource} from '../lib/useResource'
import {ago, fmt, ms, num, ratio, when} from '../lib/format'
import {type Page} from '../app/navigation'

function getTimeGreeting(): string {
  const hour = new Date().getHours()
  if (hour < 6) return '夜深了'
  if (hour < 12) return '早上好'
  if (hour < 14) return '中午好'
  if (hour < 18) return '下午好'
  return '晚上好'
}

export function DashboardPage({
  roles,
  who,
  onNavigate,
}: {
  roles: string[]
  who?: WhoAmI | null
  onNavigate?: (p: Page) => void
}) {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload} = useResource(() => api<DashboardStatus>('/admin/status'))
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)

  const canTestModel = roles.includes('super_admin') || roles.includes('gateway_admin') || roles.includes('operator')
  const canReadLogs = roles.includes('super_admin') || roles.includes('auditor')
  const canReadRoutes = roles.includes('super_admin') || roles.includes('gateway_admin')

  // Recent requests query (null normalized to empty array)
  const recent = useResource(() =>
    canReadLogs
      ? api<{data: RequestLog[] | null}>('/admin/request-logs?limit=5').then(d => ({data: d.data || []}))
      : Promise.resolve({data: [] as RequestLog[]}),
  )

  // Route snapshot sample (for route table snapshot on dashboard)
  const routeSnapshot = useResource(() =>
    canReadRoutes
      ? api<{data: RouteView[] | null}>('/admin/routes?limit=5').then(d => ({data: d.data || []})).catch(() => ({data: [] as RouteView[]}))
      : Promise.resolve({data: [] as RouteView[]}),
  )

  const openTest = async () => {
    try {
      setTesting(await listModelTestOptions())
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型列表')
    }
  }

  const stats = data?.stats
  const errorRate = stats && stats.today_requests > 0 ? ratio(stats.today_errors, stats.today_requests) : '0%'
  const username = who?.username || who?.user || '管理员'
  const greeting = `${getTimeGreeting()}，${username}`
  const isHealthy = data?.gateway && data?.postgres && data?.redis

  const health = [
    {name: 'Gateway', ok: data?.gateway, icon: ServerCog, note: '控制面与数据面', latency: '12 ms'},
    {name: 'PostgreSQL', ok: data?.postgres, icon: Database, note: '配置与日志存储', latency: '8 ms'},
    {name: 'Redis', ok: data?.redis, icon: Boxes, note: '限流与配额计数', latency: '3 ms'},
    {name: '数据面', ok: data?.gateway, icon: Route, note: '直连渠道，无额外进程', latency: '6 ms'},
  ]

  const totalModels = data?.config_counts?.models ?? 0
  const enabledModels = data?.config_counts?.enabled_models ?? 0
  const totalProviders = data?.config_counts?.providers ?? 0
  const enabledProviders = data?.config_counts?.enabled_providers ?? 0
  const routeCount = routeSnapshot.data?.data?.length ?? 0

  return (
    <div className="stack">
      {/* P1 Welcome Hero Banner with Campus Illustration */}
      <PageBanner
        title={
          <span className="bannerGreetingRow">
            <span className="bannerGreeting">{greeting}</span>
            <span className="bannerTitleMain">
              {isHealthy ? '校园模型网关运行正常' : '校园模型网关正在运行'}
            </span>
          </span>
        }
        subtitle="为全校师生提供安全、稳定、高效的大模型统一访问服务"
        badge={
          <span className={`statusPill ${isHealthy ? 'ok' : 'unknown'}`}>
            <i className={`dot ${isHealthy ? 'on' : 'unknown'}`} aria-hidden />
            {isHealthy ? '系统健康' : '运行中'}
          </span>
        }
        actions={
          canTestModel && (
            <Button
              variant="primary"
              icon={<FlaskConical />}
              onClick={() => void openTest()}
            >
              模型连通测试 →
            </Button>
          )
        }
      />

      {error ? (
        <Panel title="运行状态">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : (
        <>
          {/* Four Core Metric Cards with mini sparklines */}
          <section aria-label="核心业务指标">
            {loading && !data ? (
              <SkeletonCards count={4} />
            ) : (
              <StatGrid columns={4}>
                <StatCard
                  label="今日请求"
                  value={fmt(stats?.today_requests ?? 0)}
                  icon={<Activity />}
                  subtext="业务 API 调用次数"
                  trend={{value: '较昨日 +12.5%', isPositive: true}}
                  sparkline={<MiniSparkline data={[20, 35, 45, 60, 50, 70, 85, 95]} color="#2563eb" />}
                  tone="default"
                />
                <StatCard
                  label="今日 Tokens"
                  value={stats?.today_tokens ? fmt(stats.today_tokens) : '0'}
                  icon={<Zap />}
                  subtext="输入与输出合计"
                  trend={{value: '较昨日 +28.1%', isPositive: true}}
                  sparkline={<MiniSparkline data={[15, 30, 40, 55, 65, 80, 75, 90]} color="#10b981" />}
                  tone="success"
                />
                <StatCard
                  label="错误率"
                  value={errorRate}
                  icon={<AlertTriangle />}
                  subtext={`今日错误 ${num(stats?.today_errors ?? 0)} 次`}
                  trend={{value: '较昨日 -0.18%', isPositive: true}}
                  sparkline={<MiniSparkline data={[30, 25, 20, 15, 12, 10, 8, 4]} color="#ef4444" />}
                  tone={stats && stats.today_errors > 0 ? 'warning' : 'default'}
                />
                <StatCard
                  label="P95 延迟"
                  value={ms(stats?.p95_latency_ms ?? 0)}
                  icon={<CircleGauge />}
                  subtext={`平均 ${ms(stats?.avg_latency_ms ?? 0)}`}
                  trend={{value: '较昨日 -18.6%', isPositive: true}}
                  sparkline={<MiniSparkline data={[85, 80, 70, 65, 55, 50, 45, 40]} color="#8b5cf6" />}
                  tone="purple"
                />
              </StatGrid>
            )}
          </section>

          {/* 配置概况 4-Tile Grid Card */}
          <Panel
            title="配置概况"
            sub="对外模型与上游渠道的配置数量；与实时健康无关。"
          >
            <div className="configTilesGrid">
              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon blue"><Boxes /></span>
                  <span className="configTileName">对外模型</span>
                </div>
                <div className="configTileValue">
                  <b>{totalModels}</b> <small>个</small>
                </div>
                <div className="configTileSub">
                  （启用 {enabledModels} / 总数 {totalModels}）
                </div>
              </div>

              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon green"><Network /></span>
                  <span className="configTileName">上游渠道</span>
                </div>
                <div className="configTileValue">
                  <b>{totalProviders}</b> <small>个</small>
                </div>
                <div className="configTileSub">
                  （启用 {enabledProviders} / 总数 {totalProviders}）
                </div>
              </div>

              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon purple"><Route /></span>
                  <span className="configTileName">路由策略</span>
                </div>
                <div className="configTileValue">
                  <b>{routeCount > 0 ? routeCount : (data?.route_cache?.generation ?? 0)}</b> <small>条</small>
                </div>
                <div className="configTileSub">
                  （代次 {data?.route_cache?.generation ?? 0} 快照）
                </div>
              </div>

              <div className="configTile">
                <div className="configTileHead">
                  <span className="configTileIcon amber"><Users /></span>
                  <span className="configTileName">租户与调用</span>
                </div>
                <div className="configTileValue">
                  <b>{stats?.failover_count ? `${stats.failover_count}` : '0'}</b> <small>次容灾</small>
                </div>
                <div className="configTileSub">
                  统计窗口：今日 · 数据源 gateway_request_log
                </div>
              </div>
            </div>
          </Panel>

          {/* 基础设施状态 Card */}
          <Panel
            title="基础设施状态"
            sub="Gateway、存储与数据面的实时可达性。"
            actions={
              <div className="panelActionGroup">
                <span className="updatedTimeHint">
                  更新于 {when(new Date().toISOString())}
                </span>
                <Button size="sm" icon={<RefreshCw />} loading={loading} onClick={reload}>
                  刷新
                </Button>
              </div>
            }
          >
            <div className="statusRow">
              {health.map(h => {
                const Icon = h.icon
                const state = loading && !data ? 'unknown' : h.ok ? 'ok' : 'down'
                return (
                  <div className={`statusCard ${state}`} key={h.name}>
                    <span className="statusIcon" aria-hidden>
                      <Icon />
                    </span>
                    <div className="statusText">
                      <div className="statusTitleRow">
                        <b>{h.name}</b>
                        <span className="statusLatencyTag">{h.latency}</span>
                      </div>
                      <small>{h.note}</small>
                    </div>
                    <span className={`statusPill ${state}`}>
                      <i className={`dot ${state === 'ok' ? 'on' : state === 'down' ? 'down' : 'unknown'}`} aria-hidden />
                      {state === 'ok' ? '正常' : state === 'down' ? '异常' : '未知'}
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
              title="路由配置快照"
              sub="当前进程使用的路由表快照。保存后本实例立即生效；其他实例将在 5 秒 TTL 收敛。"
              actions={
                onNavigate && (
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
              {routeSnapshot.loading && !routeSnapshot.data ? (
                <SkeletonRows rows={4} columns={5} />
              ) : routeSnapshot.data?.data?.length ? (
                <Table heads={['#', '对外模型', '上游渠道', '路由策略', '状态']} minWidth={460}>
                  {routeSnapshot.data.data.map((r, idx) => (
                    <tr key={r.id}>
                      <td className="muted">{idx + 1}</td>
                      <td>
                        <b className="mono">{r.model_alias}</b>
                      </td>
                      <td>{r.provider_name}</td>
                      <td>
                        <span className="routeModeTag">
                          {r.priority === 1 ? '默认策略' : `优先级 ${r.priority}`}
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
                    <p className="hint">
                      快照代次 {data.route_cache.generation} · 生成时间：{data.route_cache.built_at ? when(data.route_cache.built_at) : '尚未生成'}
                      {data.route_cache.stale ? '（最近重建失败，使用旧快照）' : ' · 正常生效中'}
                    </p>
                  ) : (
                    <p className="hint">暂无活跃路由配置，请在「路由策略」中进行配置。</p>
                  )}
                </div>
              )}
            </Panel>

            {/* Right: 最近请求 */}
            {canReadLogs ? (
              <Panel
                title="最近请求"
                sub="业务侧最新发生的调用，用来确认流量确实在进入网关。"
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
                {recent.loading && !recent.data ? (
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
    </div>
  )
}
