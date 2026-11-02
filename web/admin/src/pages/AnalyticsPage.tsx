import {Route} from 'lucide-react'
import {api, type DashboardStatus} from '../api'
import {Panel} from '../components/ui/Panel'
import {Table} from '../components/ui/Table'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {TrendPanel} from '../components/charts/TrendPanel'
import {useResource} from '../lib/useResource'
import {ms, num} from '../lib/format'

/**
 * The data board answers "how has it been going", which is a different
 * question from the overview's "is it working right now".
 *
 * Splitting them is what keeps both readable: the overview is a set of states
 * with no time axis, and this page is a set of series with nothing but a time
 * axis. Mixing the two put a row of numbers, four status lights and three
 * charts on one screen, and the charts were the part nobody scrolled to.
 */
export function AnalyticsPage() {
  const {data, loading, error, errorStatus, reload} = useResource(() => api<DashboardStatus>('/admin/status'))
  const stats = data?.stats

  return (
    <div className="stack">
      <TrendPanel />

      <Panel
        title="渠道状态"
        sub="按渠道归因的请求量与成功率。"
        actions={
          <span className="panelFootnote">
            <Route aria-hidden />
            故障切换 {stats ? `${num(stats.failover_count)} 次` : '—'}
          </span>
        }
      >
        {loading && !data ? (
          <SkeletonRows rows={3} columns={4} />
        ) : error ? (
          // A failed status read used to fall through to the "no telemetry"
          // message, which states a fact about traffic rather than about the
          // request that just failed.
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : data?.provider_stats?.length ? (
          <Table heads={['渠道', '请求数', '成功率', '平均延迟']} minWidth={640}>
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
            title="暂无可归因的渠道遥测数据"
            description="产生业务请求后，这里会按渠道汇总请求量与成功率。"
          />
        )}
      </Panel>
    </div>
  )
}
