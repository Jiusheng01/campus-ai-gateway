import type {RequestLog} from '../../api'
import {Detail, TelemetryRow} from '../ui/Detail'
import {ms, num, when} from '../../lib/format'

/**
 * Request detail, split into sections.
 *
 * Telemetry fields (provider, upstream model, priority, attempts) are only
 * populated when ThinkWatch reports them. They render as 未知 rather than a
 * guess — a wrong provider name in a debugging tool is worse than a blank.
 *
 * Token figures follow the same rule, and each figure is judged on its own. A
 * request that never returned a usable `usage` block records every token field
 * as 0 — "nothing counted", not a genuine zero. But a provider may also report a
 * total without the input/output split, so a positive total does not make a zero
 * input or output real; each field shows 未知 unless it holds a value of its own.
 * That is the conservative direction: an unknown figure is honest, a fabricated
 * 0 is not (see ADR-0010).
 */
export function LogDetail({v}: {v: RequestLog}) {
  const input = v.input_tokens > 0
  const output = v.output_tokens > 0
  const total = v.total_tokens > 0
  return (
    <div className="detailStack">
      <Detail
        title="请求"
        rows={[
          ['Request ID', <span key="rid" className="mono wrap">{v.request_id}</span>],
          ['时间', when(v.created_at)],
          ['租户', v.tenant_id],
          ['API 密钥', v.api_key_id],
          ['来源 IP', v.client_ip || '—'],
        ]}
      />
      <Detail
        title="路由"
        rows={[
          ['请求模型', v.requested_model],
          ['实际模型', TelemetryRow('实际模型', v.actual_model)[1]],
          ...([
            TelemetryRow('渠道', v.provider),
            TelemetryRow('上游模型', v.upstream_model),
            TelemetryRow('线路优先级', v.route_priority),
            TelemetryRow('尝试次数', v.attempts),
          ] as [string, React.ReactNode][]),
          ['故障切换', v.failover ? '是' : '否'],
        ]}
      />
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
