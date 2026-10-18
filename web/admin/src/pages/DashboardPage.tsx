import {useState} from 'react'
import {
  Activity,
  AlertTriangle,
  Boxes,
  CircleGauge,
  Database,
  FlaskConical,
  Network,
  RefreshCw,
  Route,
  ServerCog,
  Zap,
} from 'lucide-react'
import {api, listModelTestOptions, type DashboardStatus, type ModelTestOption, type RequestLog} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards, SkeletonRows} from '../components/ui/Skeleton'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {useResource} from '../lib/useResource'
import {ago, fmt, ms, num, ratio} from '../lib/format'

/**
 * The overview answers "is it working right now", and nothing else.
 *
 * It used to also carry the trend charts and the per-channel telemetry table.
 * Those belong to the data board: they answer a question with a time axis, and
 * putting them here pushed the two things an operator checks first — the
 * numbers and the four status lights — off the first screen.
 */
export function DashboardPage({roles}: {roles: string[]}) {
  const toast = useToast()
  const confirm = useConfirm()
  const {data, loading, error, errorStatus, reload} = useResource(() => api<DashboardStatus>('/admin/status'))
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)
  const [syncing, setSyncing] = useState(false)

  // Quick actions are gated by the same allow-list the gateway uses, so the
  // dashboard never offers a button whose only possible result is a 403. A
  // gateway_admin may read the status the page reports and the operator may
  // read it too; neither may change the routing that produced it.
  const canSync = roles.includes('super_admin') || roles.includes('gateway_admin')
  const canTestModel = canSync || roles.includes('operator')
  // The recent-requests strip reads the log endpoint, which is a narrower
  // permission than the dashboard itself. A gateway_admin may see this page but
  // not that endpoint, so the request is skipped rather than allowed to 403 and
  // paint an error box onto an otherwise healthy overview.
  const canReadLogs = roles.includes('super_admin') || roles.includes('auditor')
  const recent = useResource(() =>
    canReadLogs
      ? api<{data: RequestLog[]}>('/admin/request-logs?limit=6')
      : Promise.resolve({data: [] as RequestLog[]}),
  )

  const sync = async () => {
    const ok = await confirm({
      title: '同步 ThinkWatch 配置？',
      description: '将当前渠道、模型和线路配置写入 ThinkWatch。同步后业务请求会立即按新线路路由。',
      confirmText: '确认同步',
    })
    if (!ok) return
    setSyncing(true)
    try {
      const out = await api<{providers: number; aliases: number}>('/admin/thinkwatch/sync', {method: 'POST'})
      toast.success(`ThinkWatch 同步成功：${out.providers} 个渠道 / ${out.aliases} 个对外模型`)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'ThinkWatch 同步失败')
    } finally {
      setSyncing(false)
    }
  }

  // The model list is only needed to populate the test drawer, so it is fetched
  // on demand rather than adding another request to every dashboard load.
  //
  // It comes from /admin/model-test-options rather than /admin/models: running a
  // connectivity test and editing model routes are different capabilities, and
  // an operator has the first without the second. Reading the full model
  // configuration here would have made the button 403 for the role the button
  // exists for.
  const openTest = async () => {
    try {
      setTesting(await listModelTestOptions())
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型列表')
    }
  }

  const stats = data?.stats
  const errorRate = stats && stats.today_requests > 0 ? ratio(stats.today_errors, stats.today_requests) : '0%'

  // Which data plane is running changes what the fourth status light means.
  // In direct mode there is no ThinkWatch process to be healthy or unhealthy —
  // the gateway dials the channels itself — so reporting `thinkwatch: false`
  // there would paint a red "异常" for something the deployment never started.
  const direct = data?.data_plane === 'direct'
  const health = [
    {name: 'Gateway', ok: data?.gateway, icon: ServerCog, note: '控制面与数据面'},
    {name: 'PostgreSQL', ok: data?.postgres, icon: Database, note: '配置与日志存储'},
    {name: 'Redis', ok: data?.redis, icon: Boxes, note: '限流与配额计数'},
    direct
      ? {name: '数据面', ok: data?.gateway, icon: Route, note: '直连渠道，无额外进程'}
      : {name: 'ThinkWatch', ok: data?.thinkwatch, icon: Network, note: '模型路由与故障切换'},
  ]

  // The sync action writes ThinkWatch's config file. In direct mode there is
  // no such file, so the button is removed rather than left to fail.
  const showSync = canSync && data !== undefined && !direct

  return (
    <div className="stack">
      {(showSync || canTestModel) && (
        <div className="quickActions">
          {showSync && (
            <Button icon={<RefreshCw />} loading={syncing} onClick={() => void sync()}>
              同步 ThinkWatch
            </Button>
          )}
          {canTestModel && (
            <Button variant="primary" icon={<FlaskConical />} onClick={() => void openTest()}>
              模型连通测试
            </Button>
          )}
        </div>
      )}

      {error ? (
        <Panel title="运行状态">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : (
        <>
          <section aria-label="核心业务指标">
            {loading && !data ? (
              <SkeletonCards count={4} />
            ) : (
              <div className="kpiGrid">
                <Kpi label="今日请求" value={fmt(stats?.today_requests)} icon={<Activity />} foot="业务 API 调用次数" />
                <Kpi label="今日 Tokens" value={fmt(stats?.today_tokens)} icon={<Zap />} foot="输入与输出合计" />
                <Kpi
                  label="错误率"
                  value={errorRate}
                  icon={<AlertTriangle />}
                  foot={`今日错误 ${num(stats?.today_errors)} 次`}
                  tone={stats && stats.today_errors > 0 ? 'warning' : undefined}
                />
                <Kpi label="P95 延迟" value={ms(stats?.p95_latency_ms)} icon={<CircleGauge />} foot={`平均 ${ms(stats?.avg_latency_ms)}`} />
              </div>
            )}
          </section>

          <Panel
            title="基础设施状态"
            sub={direct ? 'Gateway、存储与数据面的实时可达性。' : 'Gateway、存储与路由组件的实时可达性。'}
            actions={
              <Button size="sm" icon={<RefreshCw />} loading={loading} onClick={reload}>
                刷新
              </Button>
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
                      <b>{h.name}</b>
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

          {canReadLogs && (
            <Panel
              title="最近请求"
              sub="业务侧最新发生的调用，用来确认流量确实在进入网关。"
              actions={
                <Button size="sm" icon={<RefreshCw />} loading={recent.loading} onClick={recent.reload}>
                  刷新
                </Button>
              }
            >
              {recent.loading && !recent.data ? (
                <SkeletonRows rows={4} columns={5} />
              ) : recent.data?.data.length ? (
                <Table heads={['时间', '对外模型', '渠道', '状态', '延迟']} minWidth={620}>
                  {recent.data.data.map(r => (
                    <tr key={r.request_id}>
                      <td className="muted">{ago(r.created_at)}</td>
                      <td>
                        <b>{r.requested_model || '—'}</b>
                      </td>
                      <td>{r.provider || '—'}</td>
                      <td>
                        <span className={`badge ${r.status_code >= 400 ? 'danger' : 'success'}`}>
                          {r.status_code}
                        </span>
                      </td>
                      <td className="numeric">{ms(r.latency_ms)}</td>
                    </tr>
                  ))}
                </Table>
              ) : (
                <EmptyState
                  title="暂无调用记录"
                  description="业务请求进入网关后，这里会显示最近几条调用。完整检索请到「使用日志」。"
                />
              )}
            </Panel>
          )}
        </>
      )}

      {testing && <ModelTestDrawer models={testing} direct={direct} onClose={() => setTesting(null)} />}
    </div>
  )
}

function Kpi({
  label,
  value,
  icon,
  foot,
  tone,
}: {
  label: string
  value: string
  icon: React.ReactNode
  foot: string
  tone?: 'warning' | 'danger'
}) {
  return (
    <div className={`kpi ${tone || ''}`.trim()}>
      <span className="kpiLabel">
        {icon}
        {label}
      </span>
      <strong className="kpiValue">{value}</strong>
      <span className="kpiFoot">{foot}</span>
    </div>
  )
}
