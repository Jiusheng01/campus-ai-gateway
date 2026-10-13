import type {RequestLog} from '../../api'
import {Detail, TelemetryRow} from '../ui/Detail'
import {ms, when} from '../../lib/format'

/**
 * Request detail, split into sections.
 *
 * Telemetry fields (provider, upstream model, priority, attempts) are only
 * populated when ThinkWatch reports them. They render as 未知 rather than a
 * guess — a wrong provider name in a debugging tool is worse than a blank.
 */
export function LogDetail({v}: {v: RequestLog}) {
  return (
    <div className="detailStack">
      <Detail
        title="请求"
        rows={[
          ['Request ID', <span key="rid" className="mono wrap">{v.request_id}</span>],
          ['时间', when(v.created_at)],
          ['租户', v.tenant_id],
          ['API Key', v.api_key_id],
          ['来源 IP', v.client_ip || '—'],
        ]}
      />
      <Detail
        title="路由"
        rows={[
          ['请求模型', v.requested_model],
          ['实际模型', TelemetryRow('实际模型', v.actual_model)[1]],
          ...([
            TelemetryRow('供应商', v.provider),
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
          ['Input Tokens', v.input_tokens],
          ['Output Tokens', v.output_tokens],
          ['Total Tokens', v.total_tokens],
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
