import {useCallback, useEffect, useMemo, useState} from 'react'
import {FileClock, Search, X} from 'lucide-react'
import {
  getConsoleRequestLog,
  listConsoleKeys,
  listConsoleRequestLogs,
  type ConsoleRequestLogDetail,
  type ConsoleRequestLogPage,
} from '../api'
import {PageBanner} from '../components/ui/PageBanner'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {Detail} from '../components/ui/Detail'
import {HttpStatusBadge} from '../components/ui/Badge'
import {useResource} from '../lib/useResource'
import {ms, num, shortId, when} from '../lib/format'

const PAGE_SIZE = 50

/**
 * The filter form. Times are held as the zoneless string a datetime-local input
 * produces and converted to an instant only when the query is built, so the
 * browser never has to guess the zone on the server's behalf.
 */
type Filters = {
  from: string
  to: string
  model: string
  status: string
  api_key_id: string
}

const emptyFilters: Filters = {
  from: '',
  to: '',
  model: '',
  status: '',
  api_key_id: '',
}

function filtersToQuery(f: Filters): string {
  const q = new URLSearchParams()
  const model = f.model.trim()
  if (model) q.set('model', model)
  const status = f.status.trim()
  if (status) q.set('status', status)
  if (f.api_key_id) q.set('api_key_id', f.api_key_id)
  for (const key of ['from', 'to'] as const) {
    const raw = f[key].trim()
    if (!raw) continue
    const d = new Date(raw)
    if (!Number.isNaN(d.getTime())) q.set(key, d.toISOString())
  }
  return q.toString()
}

function activeCount(f: Filters): number {
  return Object.values(f).filter(v => v.trim() !== '').length
}

function Filter({label, children}: {label: string; children: React.ReactNode}) {
  return (
    <label className="filterField">
      <span>{label}</span>
      {children}
    </label>
  )
}

/**
 * The member's own Request Records.
 *
 * Every row comes from /api/console/request-logs, whose owner predicate is the
 * session account. The page never sends an owner or a tenant: model, status,
 * key and time can only narrow what the account already owns. Detail is fetched
 * separately because a page of records must not drag every attempt of every row
 * with it.
 */
export function ConsoleLogsPage() {
  const [filters, setFilters] = useState<Filters>(emptyFilters)
  // `applied` is what the request actually used, so typing in a box does not
  // refetch on every keystroke.
  const [applied, setApplied] = useState<Filters>(emptyFilters)
  const [offset, setOffset] = useState(0)
  const [detailID, setDetailID] = useState<string | null>(null)
  const [detail, setDetail] = useState<ConsoleRequestLogDetail | null>(null)
  const [detailError, setDetailError] = useState('')
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailNonce, setDetailNonce] = useState(0)

  // The key picker is a convenience, not an authority: a key that has been
  // deleted no longer appears here, but its records remain in the list through
  // the owner snapshot.
  const keys = useResource(listConsoleKeys)
  const keyOptions = keys.data || []

  const query = useMemo(() => filtersToQuery(applied), [applied])
  const loader = useCallback(async (): Promise<ConsoleRequestLogPage> => {
    const q = new URLSearchParams(query)
    q.set('limit', String(PAGE_SIZE))
    q.set('offset', String(offset))
    return listConsoleRequestLogs(q.toString())
  }, [query, offset])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [query, offset])
  const rows = data?.data || []
  const narrowed = activeCount(applied)

  useEffect(() => {
    if (!detailID) return
    let live = true
    setDetail(null)
    setDetailError('')
    setDetailLoading(true)
    getConsoleRequestLog(detailID)
      .then(d => {
        if (live) setDetail(d)
      })
      .catch(e => {
        if (live) setDetailError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (live) setDetailLoading(false)
      })
    return () => {
      live = false
    }
  }, [detailID, detailNonce])

  const closeDetail = () => {
    setDetailID(null)
    setDetail(null)
    setDetailError('')
    setDetailLoading(false)
  }

  const search = () => {
    setOffset(0)
    setApplied(filters)
    if (offset === 0) reload()
  }

  const reset = () => {
    setFilters(emptyFilters)
    setApplied(emptyFilters)
    setOffset(0)
    reload()
  }

  const f = (k: keyof Filters) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setFilters({...filters, [k]: e.target.value})

  return (
    <div className="stack">
      <PageBanner
        title="调用日志"
        subtitle="只显示属于你的请求记录。打开一条记录可以查看它的尝试链路与结果。"
        illustration={false}
      />

      <Panel title="我的请求记录" sub="按时间、模型、状态或 Key 筛选；这些条件都只能缩小你自己的记录范围。">
        <div className="filters">
          <Filter label="起始时间">
            <input type="datetime-local" value={filters.from} onChange={f('from')} />
          </Filter>
          <Filter label="结束时间">
            <input type="datetime-local" value={filters.to} onChange={f('to')} />
          </Filter>
          <Filter label="模型（请求或实际）">
            <input placeholder="全部模型" value={filters.model} onChange={f('model')} />
          </Filter>
          <Filter label="HTTP 状态">
            <input type="number" min={0} placeholder="任意状态" value={filters.status} onChange={f('status')} />
          </Filter>
          <Filter label="API Key">
            <select value={filters.api_key_id} onChange={f('api_key_id')}>
              <option value="">全部 API Key</option>
              {/* A key chosen before it was deleted would otherwise render as a
                  blank select, hiding the filter that is actually applied. */}
              {filters.api_key_id !== '' && !keyOptions.some(k => k.id === filters.api_key_id) && (
                <option value={filters.api_key_id}>已删除的 Key（{shortId(filters.api_key_id)}）</option>
              )}
              {keyOptions.map(k => (
                <option key={k.id} value={k.id}>
                  {k.name}（{k.key_prefix}）
                </option>
              ))}
            </select>
          </Filter>

          <div className="filterActions">
            <Button variant="primary" icon={<Search />} onClick={search}>
              查询
            </Button>
            <Button icon={<X />} disabled={narrowed === 0} onClick={reset}>
              重置{narrowed > 0 ? `（${narrowed}）` : ''}
            </Button>
          </div>
        </div>

        {loading && !data ? (
          <SkeletonRows rows={6} columns={6} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : rows.length ? (
          <Table heads={['Request ID', '模型', 'Tokens', '延迟', '状态', '时间']} minWidth={860}>
            {rows.map(r => (
              <tr key={r.request_id} className="clickRow" onClick={() => setDetailID(r.request_id)}>
                <td>
                  <code className="mono" title={r.request_id}>
                    {shortId(r.request_id)}
                  </code>
                </td>
                <td>
                  <b>{r.requested_model || '未知'}</b>
                  {r.actual_model && r.actual_model !== r.requested_model && (
                    <small className="muted"> 实际 {r.actual_model}</small>
                  )}
                </td>
                <td className="numeric">{num(r.total_tokens)}</td>
                <td className="numeric">{ms(r.latency_ms)}</td>
                <td>
                  <HttpStatusBadge code={r.status_code} />
                </td>
                <td>{when(r.created_at)}</td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState
            icon={<FileClock />}
            title={narrowed > 0 ? '没有匹配的调用记录' : '还没有调用记录'}
            description={
              narrowed > 0
                ? '调整筛选条件或重置后再试。'
                : '你的账号还没有产生任何请求。使用自己的 API Key 发起调用后，记录会出现在这里。'
            }
          />
        )}

        {!error && (rows.length > 0 || offset > 0) && (
          <div className="pager">
            <span>
              Offset {offset} · 本页 {rows.length} 条
            </span>
            <div className="pagerActions">
              <Button size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>
                上一页
              </Button>
              <Button size="sm" disabled={rows.length < PAGE_SIZE} onClick={() => setOffset(offset + PAGE_SIZE)}>
                下一页
              </Button>
            </div>
          </div>
        )}
      </Panel>

      {detailID && (
        <Drawer title="请求详情" subtitle={detailID} onClose={closeDetail}>
          {detailLoading ? (
            <p className="muted">加载中…</p>
          ) : detailError ? (
            <ErrorState message={detailError} onRetry={() => setDetailNonce(n => n + 1)} />
          ) : detail ? (
            <ConsoleLogDetail v={detail} keyName={keyName(keyOptions, detail.api_key_id)} />
          ) : null}
        </Drawer>
      )}
    </div>
  )
}

function keyName(keys: {id: string; name: string; key_prefix: string}[], id: string): string {
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
  const input = v.input_tokens > 0
  const output = v.output_tokens > 0
  const total = v.total_tokens > 0
  const attempts = v.attempt_chain || []

  return (
    <div className="detailStack">
      <Detail
        title="请求"
        rows={[
          ['Request ID', <span key="rid" className="mono wrap">{v.request_id}</span>],
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
