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
import {api, type DashboardStatus, type ModelConfig} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards, SkeletonRows} from '../components/ui/Skeleton'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {useResource} from '../lib/useResource'
import {fmt, ms, num, ratio} from '../lib/format'

/**
 * Dashboard, arranged in three layers so the operator reads it top-down:
 * business KPIs, then infrastructure, then per-supplier telemetry.
 */
export function DashboardPage() {
  const toast = useToast()
  const confirm = useConfirm()
  const {data, loading, error, errorStatus, reload} = useResource(() => api<DashboardStatus>('/admin/status'))
  const [testing, setTesting] = useState<ModelConfig[] | null>(null)
  const [syncing, setSyncing] = useState(false)

  const sync = async () => {
    const ok = await confirm({
      title: '同步 ThinkWatch 配置？',
      description: '将当前供应商、模型和线路配置写入 ThinkWatch。同步后业务请求会立即按新线路路由。',
      confirmText: '确认同步',
    })
    if (!ok) return
    setSyncing(true)
    try {
      const out = await api<{providers: number; aliases: number}>('/admin/thinkwatch/sync', {method: 'POST'})
      toast.success(`ThinkWatch 同步成功：${out.providers} 个供应商 / ${out.aliases} 个对外模型`)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'ThinkWatch 同步失败')
    } finally {
      setSyncing(false)
    }
  }

  // The model list is only needed to populate the test drawer, so it is fetched
  // on demand rather than adding another request to every dashboard load.
  const openTest = async () => {
    try {
      const d = await api<{data: ModelConfig[]}>('/admin/models')
      setTesting(d.data || [])
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型列表')
    }
  }

  const stats = data?.stats
  const errorRate = stats && stats.today_requests > 0 ? ratio(stats.today_errors, stats.today_requests) : '0%'

  const health = [
    {name: 'Gateway', ok: data?.gateway, icon: ServerCog, note: '控制面与数据面'},
    {name: 'PostgreSQL', ok: data?.postgres, icon: Database, note: '配置与日志存储'},
    {name: 'Redis', ok: data?.redis, icon: Boxes, note: '限流与配额计数'},
    {name: 'ThinkWatch', ok: data?.thinkwatch, icon: Network, note: '模型路由与故障切换'},
  ]

  return (
    <div className="stack">
      <div className="quickActions">
        <Button icon={<RefreshCw />} loading={syncing} onClick={() => void sync()}>
          同步 ThinkWatch
        </Button>
        <Button variant="primary" icon={<FlaskConical />} onClick={() => void openTest()}>
          模型连通测试
        </Button>
      </div>

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
            sub="Gateway、存储与路由组件的实时可达性。"
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

          <Panel
            title="供应商状态"
            sub="按供应商归因的请求量与成功率，数据来自线路遥测；未知数据不会被猜测。"
            actions={
              <span className="panelFootnote">
                <Route aria-hidden />
                故障切换 {num(stats?.failover_count)} 次
              </span>
            }
          >
            {loading && !data ? (
              <SkeletonRows rows={3} columns={4} />
            ) : data?.provider_stats?.length ? (
              <Table heads={['供应商', '请求数', '成功率', '平均延迟']} minWidth={640}>
                {data.provider_stats.map(p => (
                  <tr key={p.provider}>
                    <td>
                      <b>{p.provider}</b>
                    </td>
                    <td className="numeric">{num(p.requests)}</td>
                    <td className="numeric">{p.success_rate.toFixed(1)}%</td>
                    <td className="numeric">{ms(p.avg_latency_ms)}</td>
                  </tr>
                ))}
              </Table>
            ) : (
              <EmptyState
                title="暂无可归因的供应商遥测数据"
                description="产生业务请求后，这里会按供应商汇总请求量与成功率。"
              />
            )}
          </Panel>
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
