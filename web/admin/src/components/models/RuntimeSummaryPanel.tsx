import {useCallback, useState} from 'react'
import {Activity} from 'lucide-react'
import {api} from '../../api'
import {Button} from '../ui/Button'
import {EmptyState, ErrorState} from '../ui/EmptyState'
import {SkeletonRows} from '../ui/Skeleton'
import {Table} from '../ui/Table'
import {Notice} from '../ui/Field'
import {useResource} from '../../lib/useResource'
import {ms, num, when} from '../../lib/format'

type Totals = {requests: number; errors: number; success_rate: number; p95_latency_ms: number; failover: number; tokens: number}
type Recent = {
  request_id: string; created_at: string; requested_model: string; actual_model: string
  provider: string; upstream_model: string; route_priority: number; status_code: number
  error_code: string; attempts: number; failover: boolean; total_tokens: number; latency_ms: number
}
type ModelSummary = {alias: string; from: string; to: string; totals: Totals; recent: Recent[]}
type ProviderSummary = {provider: string; from: string; to: string; totals: Totals; attempts: {served: number; failed: number; skipped: number}; recent: Recent[]}

export type LogSeed = {model?: string; provider?: string; tenant_id?: string; api_key_id?: string}

/**
 * Redacted runtime summary for a model or channel. It deliberately has no
 * tenant, key or client-IP column; raw logs remain a separate capability.
 */
export function RuntimeSummaryPanel({
  kind,
  id,
  canOpenRawLogs,
  onOpenLogs,
}: {
  kind: 'model' | 'provider'
  id: string
  canOpenRawLogs: boolean
  onOpenLogs?: (filters: LogSeed) => void
}) {
  const [hours, setHours] = useState(24)
  const loader = useCallback(async () => {
    const path = kind === 'model'
      ? `/admin/models/${id}/runtime-summary?hours=${hours}`
      : `/admin/providers/${id}/runtime-summary?hours=${hours}`
    return api<ModelSummary | ProviderSummary>(path)
  }, [kind, id, hours])
  const {data, loading, error, errorStatus, reload} = useResource(loader, [kind, id, hours])
  const recent = data?.recent || []
  const attempts = data && 'attempts' in data ? data.attempts : null

  return (
    <div className="drawerStack">
      <Notice>
        仅展示该{kind === 'model' ? '模型' : '渠道'}范围的脱敏运行摘要，不含租户 ID、API Key ID、客户端 IP、问题或回答正文。
        分母是所选时间窗口内的请求记录；时间为 UTC。
      </Notice>
      <div className="quickActions">
        <select value={hours} onChange={e => setHours(+e.target.value)} aria-label="统计窗口">
          <option value={24}>最近 24 小时</option>
          <option value={168}>最近 7 天</option>
        </select>
        {canOpenRawLogs && onOpenLogs && (
          <Button
            onClick={() => onOpenLogs(kind === 'model' ? {model: data && 'alias' in data ? data.alias : undefined} : {provider: data && 'provider' in data ? data.provider : undefined})}
          >
            打开原始调用日志
          </Button>
        )}
        {!canOpenRawLogs && <span className="muted">原始调用日志仅超级管理员与审计员可读。</span>}
      </div>

      {loading && !data ? (
        <SkeletonRows rows={4} columns={6} />
      ) : error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : data ? (
        <>
          <div className="miniCardRows">
            <div><span>请求</span><b>{num(data.totals.requests)}</b></div>
            <div><span>错误</span><b>{num(data.totals.errors)}</b></div>
            <div><span>成功率</span><b>{data.totals.success_rate.toFixed(1)}%</b></div>
            <div><span>P95 延迟</span><b>{ms(data.totals.p95_latency_ms)}</b></div>
            <div><span>故障切换</span><b>{num(data.totals.failover)}</b></div>
            <div><span>Tokens</span><b>{num(data.totals.tokens)}</b></div>
          </div>
          <p className="hint">窗口：{when(data.from)} – {when(data.to)}（UTC）。没有数据时显示“暂无数据”，不以 0 冒充健康。</p>
          {attempts && (
            <p className="hint">
              尝试结果：成功 {attempts.served} · 失败 {attempts.failed} · 跳过（熔断）{attempts.skipped}。
              成功/失败只统计真正发起的尝试；熔断跳过不计入。
            </p>
          )}
          {recent.length ? (
            <Table heads={['时间', 'Request ID', '请求模型', '渠道', '上游模型', '状态', '尝试', '延迟']} minWidth={980}>
              {recent.map(r => (
                <tr key={r.request_id}>
                  <td>{when(r.created_at)}</td>
                  <td className="mono"><span className="cut" title={r.request_id}>{r.request_id}</span></td>
                  <td className="mono">{r.requested_model || '—'}</td>
                  <td>{r.provider || '—'}</td>
                  <td className="mono">{r.upstream_model || '—'}</td>
                  <td>{r.status_code}{r.error_code ? ` · ${r.error_code}` : ''}</td>
                  <td className="numeric">{r.attempts}{r.failover ? ' · failover' : ''}</td>
                  <td className="numeric">{ms(r.latency_ms)}</td>
                </tr>
              ))}
            </Table>
          ) : (
            <EmptyState icon={<Activity />} title="暂无数据" description="所选窗口内没有该范围的请求记录。" />
          )}
        </>
      ) : null}
    </div>
  )
}
