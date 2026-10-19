import {useState} from 'react'
import {
  Activity,
  AlertTriangle,
  Boxes,
  CircleGauge,
  Database,
  FlaskConical,
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
  const {data, loading, error, errorStatus, reload} = useResource(() => api<DashboardStatus>('/admin/status'))
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)

  // Quick actions are gated by the same allow-list the gateway uses, so the
  // dashboard never offers a button whose only possible result is a 403.
  const canTestModel = roles.includes('super_admin') || roles.includes('gateway_admin') || roles.includes('operator')
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

  // There is one data plane, and the gateway dials the channels itself, so the
  // fourth status light reports on that rather than on a process the deployment
  // never started.
  const health = [
    {name: 'Gateway', ok: data?.gateway, icon: ServerCog, note: '控制面与数据面'},
    {name: 'PostgreSQL', ok: data?.postgres, icon: Database, note: '配置与日志存储'},
    {name: 'Redis', ok: data?.redis, icon: Boxes, note: '限流与配额计数'},
    {name: '数据面', ok: data?.gateway, icon: Route, note: '直连渠道，无额外进程'},
  ]

  return (
    <div className="stack">
      {canTestModel && (
        <div className="quickActions">
          <Button variant="primary" icon={<FlaskConical />} onClick={() => void openTest()}>
            模型连通测试
          </Button>
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
            sub="Gateway、存储与数据面的实时可达性。"
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

      {testing && <ModelTestDrawer models={testing} onClose={() => setTesting(null)} />}
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
