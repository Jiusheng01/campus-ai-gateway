import {useEffect, useState} from 'react'
import {Copy} from 'lucide-react'
import {getConsoleRequestLog, type ConsoleRequestLogDetail} from '../../api'
import {Drawer} from '../ui/Drawer'
import {Panel} from '../ui/Panel'
import {EmptyState, ErrorState} from '../ui/EmptyState'
import {Table} from '../ui/Table'
import {Detail} from '../ui/Detail'
import {HttpStatusBadge} from '../ui/Badge'
import {useToast} from '../ui/Toast'
import {copyText, ms, num, shortId, when} from '../../lib/format'

type KeyLabel = {id: string; name: string; key_prefix: string}

/**
 * The drawer behind one Request Record.
 *
 * Both the personal log page and the overview's "recent records" table open
 * the same drawer, and it fetches the detail itself: a page of records must not
 * drag every attempt chain with it, and the two callers would otherwise keep
 * two copies of the same loading/error state. The endpoint binds the session's
 * owner id, so a record belonging to another Account answers 404 and is
 * rendered as an error rather than an empty drawer.
 */
export function ConsoleLogDrawer({
  requestID,
  keys,
  onClose,
}: {
  requestID: string
  keys: KeyLabel[]
  onClose: () => void
}) {
  const [detail, setDetail] = useState<ConsoleRequestLogDetail | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    let live = true
    setDetail(null)
    setError('')
    setLoading(true)
    getConsoleRequestLog(requestID)
      .then(d => {
        if (live) setDetail(d)
      })
      .catch(e => {
        if (live) setError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (live) setLoading(false)
      })
    return () => {
      live = false
    }
  }, [requestID, nonce])

  return (
    <Drawer title="请求详情" subtitle={requestID} onClose={onClose}>
      {loading ? (
        <p className="muted">加载中…</p>
      ) : error ? (
        <ErrorState message={error} onRetry={() => setNonce(n => n + 1)} />
      ) : detail ? (
        <ConsoleLogDetail v={detail} keyName={keyName(keys, detail.api_key_id)} />
      ) : null}
    </Drawer>
  )
}

/**
 * Resolve an api_key_id to a human label. A deleted key is still worth naming:
 * the record survives through the owner snapshot, so the id is the only handle
 * left and printing it is more honest than an empty cell.
 */
function keyName(keys: KeyLabel[], id: string): string {
  const key = keys.find(k => k.id === id)
  return key ? `${key.name}（${key.key_prefix}）` : id ? `已删除的 Key（${shortId(id)}）` : '—'
}

/**
 * The detail body. Telemetry that the record does not carry reads 未知 rather
 * than zero: a request that never returned a usage block did not use zero
 * tokens, nothing was counted. The attempt chain is the one part that can say
 * which channel was tried and why the gateway moved on.
 */
function ConsoleLogDetail({v, keyName}: {v: ConsoleRequestLogDetail; keyName: string}) {
  const toast = useToast()
  const input = v.input_tokens > 0
  const output = v.output_tokens > 0
  const total = v.total_tokens > 0
  const attempts = v.attempt_chain || []

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
          ['请求模型', <span key="requested" className="mono">{v.requested_model || '未知'}</span>],
          ['响应模型', <span key="actual" className="mono">{v.actual_model || '未知'}</span>],
          ['API Key', keyName],
        ]}
      />

      <Detail
        title="用量"
        rows={[
          ['Input（Prompt）Tokens', input ? <span key="in" className="mono">{num(v.input_tokens)}</span> : <span key="in" className="unknown">未知</span>],
          ['Output（Completion）Tokens', output ? <span key="out" className="mono">{num(v.output_tokens)}</span> : <span key="out" className="unknown">未知</span>],
          ['Total Tokens', total ? <span key="total" className="mono">{num(v.total_tokens)}</span> : <span key="total" className="unknown">未知</span>],
        ]}
      />

      <Detail
        title="结果"
        rows={[
          ['HTTP 状态', <HttpStatusBadge key="status" code={v.status_code} />],
          ['错误码', v.error_code || '—'],
          ['故障切换', v.failover ? '是' : '否'],
          ['总延迟', ms(v.latency_ms)],
        ]}
      />

      <Panel title="尝试链" sub="本次请求按顺序打过的每一条线路，以及各自的结果。">
        {attempts.length ? (
          <Table heads={['#', '渠道', '上游模型', '结果', '状态', '错误分类', '耗时']} minWidth={760}>
            {attempts.map(a => (
              <tr key={a.attempt_no}>
                <td className="numeric">{a.attempt_no}</td>
                <td>{a.provider || '—'}</td>
                <td className="mono">{a.upstream_model || '—'}</td>
                <td>{outcomeLabel(a.outcome)}</td>
                <td className="numeric">{a.status_code > 0 ? a.status_code : '—'}</td>
                <td>{a.error_kind || '—'}</td>
                <td className="numeric">{ms(a.latency_ms)}</td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState title="暂无尝试记录" description="这条请求没有留下上游尝试，通常是请求在进入上游前就被拒绝。" />
        )}
      </Panel>
    </div>
  )
}

function outcomeLabel(outcome: string): string {
  switch (outcome) {
    case 'served':
      return '成功'
    case 'skipped':
      return '跳过（熔断）'
    default:
      return '失败'
  }
}
