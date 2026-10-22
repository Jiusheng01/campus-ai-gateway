import {useEffect, useState} from 'react'
import {Copy} from 'lucide-react'
import {api, type RequestAttempt, type RequestLog} from '../../api'
import {Detail, TelemetryRow} from '../ui/Detail'
import {Badge} from '../ui/Badge'
import {EmptyState} from '../ui/EmptyState'
import {Panel} from '../ui/Panel'
import {Table} from '../ui/Table'
import {useToast} from '../ui/Toast'
import {copyText, ms, num, when} from '../../lib/format'

/**
 * Request detail, split into sections.
 *
 * Telemetry fields (provider, upstream model, priority, attempts) are only
 * populated when the data plane reports them. They render as 未知 rather than a
 * guess — a wrong provider name in a debugging tool is worse than a blank.
 *
 * Token figures follow the same rule, and each figure is judged on its own. A
 * request that never returned a usable `usage` block records every token field
 * as 0 — "nothing counted", not a genuine zero. But a provider may also report a
 * total without the input/output split, so a positive total does not make a zero
 * input or output real; each field shows 未知 unless it holds a value of its own.
 * That is the conservative direction: an unknown figure is honest, a fabricated
 * 0 is not (see ADR-0010).
 *
 * The attempt chain is fetched on open rather than carried in the list, because
 * a page of fifty requests should not drag every attempt of every row with it.
 */
export function LogDetail({v}: {v: RequestLog}) {
  const toast = useToast()
  const input = v.input_tokens > 0
  const output = v.output_tokens > 0
  const total = v.total_tokens > 0
  const [attempts, setAttempts] = useState<RequestAttempt[] | null>(null)
  // A read that failed is not the same fact as a chain the server says is
  // empty: one is an outage, the other is a request that produced no lines.
  const [attemptsError, setAttemptsError] = useState('')

  useEffect(() => {
    let live = true
    setAttempts(null)
    setAttemptsError('')
    api<{data: RequestAttempt[]}>(`/admin/request-logs/${encodeURIComponent(v.request_id)}/attempts`)
      .then(d => {
        if (live) setAttempts(d.data || [])
      })
      .catch((e: unknown) => {
        if (!live) return
        setAttempts([])
        setAttemptsError(e instanceof Error ? e.message : '无法读取尝试链')
      })
    return () => {
      live = false
    }
  }, [v.request_id])

  const copyRequestID = async () => {
    if (await copyText(v.request_id)) toast.success('Request ID 已复制')
    else toast.error('复制失败，请手动选择并复制 Request ID')
  }

  return (
    <div className="detailStack">
      <Detail
        title="请求"
        rows={[
          [
            'Request ID',
            <span key="rid" className="requestIdRow">
              <span className="mono wrap">{v.request_id}</span>
              <button
                type="button"
                className="tableActionLink"
                title={`复制 ${v.request_id}`}
                aria-label="复制 Request ID"
                onClick={() => void copyRequestID()}
              >
                <Copy aria-hidden />
                复制
              </button>
            </span>,
          ],
          ['时间', when(v.created_at)],
          ['租户', v.tenant_id],
          ['API 密钥', v.api_key_id],
          ['来源 IP', v.client_ip || '—'],
        ]}
      />
      {/* One glanceable answer to "which model actually ran, and did we
          switch?". The fields below remain the authoritative telemetry; this
          block only composes them, so a missing value still reads 未知. */}
      <Detail
        title="调用链路"
        rows={[
          ['客户端请求', <span key="requested" className="mono wrap">{v.requested_model || '未知'}</span>],
          ['实际渠道', <span key="provider" className="mono">{v.provider || '未知'}</span>],
          ['实际上游模型', <span key="upstream" className="mono wrap">{v.upstream_model || '未知'}</span>],
          ['是否切换', v.failover ? <Badge key="failover" tone="warning">已故障切换</Badge> : <Badge key="failover" tone="success">未切换</Badge>],
        ]}
      />

      <Detail
        title="路由"
        rows={[
          ['请求模型（对外 Alias）', v.requested_model],
          ['响应模型', TelemetryRow('响应模型', v.actual_model)[1]],
          ...([
            TelemetryRow('实际渠道', v.provider),
            TelemetryRow('实际上游模型', v.upstream_model),
            TelemetryRow('线路优先级', v.route_priority),
            TelemetryRow('尝试次数', v.attempts),
          ] as [string, React.ReactNode][]),
          ['故障切换', v.failover ? '是' : '否'],
        ]}
      />

      <section aria-label="尝试链">
        <Panel
          title="尝试链"
          sub="本次请求按服务端记录的顺序打过的每一条线路。熔断跳过的线路没有真的发出请求，单独标记。"
        >
          <AttemptVerdict v={v} attempts={attempts} failed={Boolean(attemptsError)} />
          {attempts === null ? (
            <p className="muted">加载中…</p>
          ) : attemptsError ? (
            <p className="filterNote" role="alert">尝试链读取失败：{attemptsError}。当前请求的事实仍以上方为准。</p>
          ) : attempts.length ? (
            <Table heads={['#', '渠道', '上游模型', '结果', '状态', '错误分类', '耗时']} minWidth={800}>
              {attempts.map(a => (
                <tr key={a.attempt_no}>
                  <td className="numeric">{a.attempt_no}</td>
                  <td>{a.provider || '—'}</td>
                  <td className="mono">{a.upstream_model || '—'}</td>
                  <td>
                    <span className={`statusChip ${attemptTone(a)}`}>
                      <i className={`dot ${attemptTone(a) === 'ok' ? 'on' : attemptTone(a) === 'down' ? 'down' : 'unknown'}`} aria-hidden />
                      {outcomeLabel(a)}
                    </span>
                  </td>
                  <td className="numeric">{a.status_code > 0 ? a.status_code : '—'}</td>
                  <td>{a.error_kind || '—'}</td>
                  <td className="numeric">{a.outcome === 'skipped' ? '未发起' : ms(a.latency_ms)}</td>
                </tr>
              ))}
            </Table>
          ) : (
            <EmptyState title="暂无尝试记录" description="直连数据面会为每次上游尝试留下一行；熔断跳过会单独标记。" />
          )}
        </Panel>
      </section>

      <Detail
        title="用量"
        rows={[
          ['Input（Prompt）Tokens', input ? <span className="mono">{num(v.input_tokens)}</span> : <span className="unknown">未知</span>],
          ['Output（Completion）Tokens', output ? <span className="mono">{num(v.output_tokens)}</span> : <span className="unknown">未知</span>],
          ['Total Tokens', total ? <span className="mono">{num(v.total_tokens)}</span> : <span className="unknown">未知</span>],
        ]}
      />
      <Detail
        title="时延"
        rows={[
          ['总延迟', ms(v.latency_ms)],
          ['流式响应', v.stream ? '是' : '否'],
        ]}
      />
      <Detail
        title="结果"
        rows={[
          ['HTTP 状态', v.status_code],
          ['错误码', v.error_code || '—'],
        ]}
      />
    </div>
  )
}

// outcomeLabel renders an attempt's outcome, folding the error classification in
// where it explains the failure.
function outcomeLabel(a: RequestAttempt): string {
  switch (a.outcome) {
    case 'served':
      return '成功'
    case 'skipped':
      return '熔断跳过'
    default:
      return '失败'
  }
}

function attemptTone(a: RequestAttempt): 'ok' | 'down' | 'unknown' {
  if (a.outcome === 'served') return 'ok'
  if (a.outcome === 'skipped') return 'unknown'
  return 'down'
}

/**
 * What the attempt chain means for the request as a whole.
 *
 * `failover` in the record says the data plane tried another line, which is
 * not the same as the request recovering: a chain where every line failed is
 * also a failover. The verdict therefore reads the outcomes, and says nothing
 * while the chain is still loading or could not be read.
 */
function AttemptVerdict({v, attempts, failed}: {v: RequestLog; attempts: RequestAttempt[] | null; failed: boolean}) {
  // A read that failed says nothing about the request: claiming "no attempts"
  // would state a fact the console does not have.
  if (failed || attempts === null) return null
  const tried = attempts.filter(a => a.outcome !== 'skipped')
  const served = attempts.some(a => a.outcome === 'served')
  const timeline = (list: RequestAttempt[]) => (
    <ol className="attemptTimeline">
      {list.map(a => (
        <li key={a.attempt_no} className={`attemptItem ${attemptTone(a)}`}>
          <span className="attemptOrder">第 {a.attempt_no} 次尝试</span>
          <span className="attemptOutcome">{outcomeLabel(a)}</span>
          <span className="attemptWhere">{(a.provider || '未知渠道') + ' · ' + (a.upstream_model || '未知上游模型')}</span>
        </li>
      ))}
    </ol>
  )
  if (tried.length <= 1) {
    return (
      <div className="attemptVerdict">
        <b>{served ? '首次尝试即成功' : attempts.length ? '唯一的尝试没有成功' : '没有可用的尝试记录'}</b>
        {attempts.length > 0 && timeline(attempts)}
      </div>
    )
  }
  return (
    <div className="attemptVerdict">
      <b>{served ? '故障切换成功' : '故障切换后仍失败'}</b>
      <span>
        {v.failover
          ? `服务端记录本次请求尝试了 ${tried.length} 条线路${attempts.length > tried.length ? `，另有 ${attempts.length - tried.length} 条因熔断跳过` : ''}。`
          : `尝试链有 ${tried.length} 条被实际发起的线路。`}
        {' '}“发生故障切换”表示换过线路，不代表最终成功。
      </span>
      {timeline(attempts)}
    </div>
  )
}
