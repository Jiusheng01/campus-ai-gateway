import {useCallback, useMemo, useState} from 'react'
import {Copy, Download, FileClock, Search, X} from 'lucide-react'
import {api, download, listRequestLogFacets, summarizeRequestLogs, type RequestLog, type RequestLogFacets, type RequestLogSummary} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, HttpStatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {LogDetail} from '../components/logs/LogDetail'
import {LogSummaryStrip} from '../components/logs/LogSummaryStrip'
import {useResource} from '../lib/useResource'
import {copyText, ms, num, shortId, when} from '../lib/format'

const PAGE_SIZE = 50

/**
 * The filter form. `from` and `to` hold whatever a `datetime-local` input
 * produces, which is a zoneless local string; it is converted to an instant only
 * when the query is built, so the value the operator sees is the value they
 * typed and the value the server receives is unambiguous.
 */
type Filters = {
  model: string
  provider: string
  status: string
  request_id: string
  failover: string
  tenant_id: string
  api_key_id: string
  from: string
  to: string
}

const emptyFilters: Filters = {
  model: '',
  provider: '',
  status: '',
  request_id: '',
  failover: '',
  tenant_id: '',
  api_key_id: '',
  from: '',
  to: '',
}

/**
 * Turns the filter form into a query string.
 *
 * Shared by the table, the summary and the CSV export, which is what keeps the
 * three describing the same set of rows. `datetime-local` yields local wall time
 * with no offset, and `new Date` reads exactly that shape as local — so
 * toISOString here is the step that gives the server a real instant instead of a
 * string it would have to interpret in a zone it does not know.
 */
function filtersToQuery(f: Filters): URLSearchParams {
  const q = new URLSearchParams()
  const text: [keyof Filters, string][] = [
    ['model', 'model'],
    ['provider', 'provider'],
    ['status', 'status'],
    ['request_id', 'request_id'],
    ['failover', 'failover'],
    ['tenant_id', 'tenant_id'],
    ['api_key_id', 'api_key_id'],
  ]
  for (const [key, param] of text) {
    const v = f[key].trim()
    if (v) q.set(param, v)
  }
  for (const key of ['from', 'to'] as const) {
    const raw = f[key].trim()
    if (!raw) continue
    const d = new Date(raw)
    if (!Number.isNaN(d.getTime())) q.set(key, d.toISOString())
  }
  return q
}

/** Number of filters currently narrowing the table, for the reset affordance. */
function activeCount(f: Filters): number {
  return Object.values(f).filter(v => v.trim() !== '').length
}

/**
 * One filter cell.
 *
 * Every control carries its label rather than relying on a placeholder: the
 * placeholders disappear as soon as a value is typed, which leaves a row of
 * nine anonymous boxes, and a `datetime-local` input shows no placeholder at all
 * — two of them side by side are indistinguishable without one. Wrapping the
 * input in the label also makes the caption a click target for it.
 */
function Filter({label, children}: {label: string; children: React.ReactNode}) {
  return (
    <label className="filterField">
      <span>{label}</span>
      {children}
    </label>
  )
}

export function RequestLogsPage() {
  const toast = useToast()
  const [filters, setFilters] = useState<Filters>(emptyFilters)
  // `applied` is what the request actually used, so typing in a filter box does
  // not refetch on every keystroke.
  const [applied, setApplied] = useState<Filters>(emptyFilters)
  const [offset, setOffset] = useState(0)
  const [detail, setDetail] = useState<RequestLog | null>(null)

  const query = useMemo(() => filtersToQuery(applied), [applied])
  const queryString = query.toString()

  const loader = useCallback(async () => {
    const q = new URLSearchParams(queryString)
    q.set('limit', String(PAGE_SIZE))
    q.set('offset', String(offset))
    // The summary and the page share one query string, so the numbers above the
    // table can never describe a different selection than the table below it.
    const [page, summary] = await Promise.all([
      api<{data: RequestLog[]}>(`/admin/request-logs?${q}`),
      summarizeRequestLogs(queryString),
    ])
    return {rows: page.data || [], summary}
  }, [queryString, offset])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [queryString, offset])
  const rows = data?.rows || []
  const summary: RequestLogSummary | null = data?.summary || null

  // The option lists are a separate resource: they change only when traffic
  // appears from a new tenant or key, so refetching them on every filter change
  // would be pure noise. A failure here is not fatal — the page still works, it
  // just has text inputs where the pickers would be — but it is said out loud
  // rather than silently swapping one control for another.
  const facets = useResource<RequestLogFacets>(() => listRequestLogFacets())

  const search = () => {
    setOffset(0)
    setApplied(filters)
    if (offset === 0) reload()
  }

  const reset = () => {
    setFilters(emptyFilters)
    setApplied(emptyFilters)
    setOffset(0)
  }

  const exportCsv = async () => {
    try {
      await download(`/admin/request-logs/export?${query}`, 'campus-gateway-request-logs.csv')
      toast.success('使用日志已导出')
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '导出失败')
    }
  }

  const copyId = async (id: string) => {
    if (await copyText(id)) toast.success('Request ID 已复制')
    else toast.error('复制失败')
  }

  const f = (k: keyof Filters) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setFilters({...filters, [k]: e.target.value})

  const narrowed = activeCount(applied)
  // A key list narrowed to the chosen tenant: the tenant and key filters are
  // related, and offering every key while one tenant is selected invites a
  // combination that can only ever return nothing.
  const keyOptions = (facets.data?.keys || []).filter(
    k => !applied.tenant_id || k.tenant_id === applied.tenant_id,
  )

  return (
    <>
      <Panel
        title="使用日志"
        sub="一次业务请求对应一行。点击任意一行查看路由、用量与时延详情。"
        actions={
          <Button icon={<Download />} onClick={() => void exportCsv()}>
            导出 CSV
          </Button>
        }
      >
        <div className="filters">
          <Filter label="模型">
            <input placeholder="全部模型" value={filters.model} onChange={f('model')} />
          </Filter>
          <Filter label="渠道">
            <input placeholder="全部渠道" value={filters.provider} onChange={f('provider')} />
          </Filter>
          <Filter label="HTTP 状态">
            <input placeholder="任意状态" value={filters.status} onChange={f('status')} />
          </Filter>
          <Filter label="Request ID">
            <input placeholder="精确匹配" value={filters.request_id} onChange={f('request_id')} />
          </Filter>
          <Filter label="故障切换">
            <select value={filters.failover} onChange={f('failover')}>
              <option value="">全部</option>
              <option value="true">发生故障切换</option>
              <option value="false">未发生故障切换</option>
            </select>
          </Filter>

          {facets.data?.tenants?.length ? (
            <Filter label="租户">
              <select value={filters.tenant_id} onChange={f('tenant_id')}>
                <option value="">全部租户</option>
                {facets.data.tenants.map(t => (
                  <option key={t.id} value={t.id}>
                    {t.name}（{t.code}）
                  </option>
                ))}
              </select>
            </Filter>
          ) : (
            <Filter label="租户 ID">
              <input placeholder="UUID" value={filters.tenant_id} onChange={f('tenant_id')} />
            </Filter>
          )}

          {keyOptions.length ? (
            <Filter label="API 密钥">
              <select value={filters.api_key_id} onChange={f('api_key_id')}>
                <option value="">全部 API 密钥</option>
                {keyOptions.map(k => (
                  <option key={k.id} value={k.id}>
                    {k.name}（{k.key_prefix}）
                  </option>
                ))}
              </select>
            </Filter>
          ) : (
            <Filter label="API 密钥 ID">
              <input placeholder="UUID" value={filters.api_key_id} onChange={f('api_key_id')} />
            </Filter>
          )}

          <Filter label="起始时间">
            <input type="datetime-local" value={filters.from} onChange={f('from')} />
          </Filter>
          <Filter label="结束时间">
            <input type="datetime-local" value={filters.to} onChange={f('to')} />
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

        {facets.error && (
          <p className="filterNote">
            无法读取租户与 API 密钥列表（{facets.error}），已改为直接填写 ID。
          </p>
        )}

        <LogSummaryStrip summary={summary} loading={loading} />

        {loading && !data ? (
          <SkeletonRows rows={8} columns={8} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : rows.length ? (
          <Table heads={['时间', '状态', '模型', '渠道', 'Tokens', '延迟', '故障切换', 'Request ID']} minWidth={1040}>
            {rows.map(r => (
              <tr key={r.request_id} className="clickRow" onClick={() => setDetail(r)}>
                <td>{when(r.created_at)}</td>
                <td>
                  <HttpStatusBadge code={r.status_code} />
                </td>
                <td>
                  <b>{r.requested_model}</b>
                </td>
                <td>{r.provider || <span className="unknown">未知</span>}</td>
                <td className="numeric">{num(r.total_tokens)}</td>
                <td className="numeric">{ms(r.latency_ms)}</td>
                <td>
                  {r.failover ? <Badge tone="warning">故障切换</Badge> : <span className="muted">—</span>}
                </td>
                <td>
                  <button
                    className="mono copyCell"
                    title={`复制 ${r.request_id}`}
                    onClick={e => {
                      e.stopPropagation()
                      void copyId(r.request_id)
                    }}
                  >
                    {shortId(r.request_id)}
                    <Copy aria-hidden />
                  </button>
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState icon={<FileClock />} title="当前筛选条件下没有请求记录" description="调整筛选条件或重置后重试。" />
        )}

        <div className="pager">
          <span>
            Offset {offset} · 本页 {rows.length} 条
            {summary ? ` · 合计 ${num(summary.requests)} 条` : ''}
          </span>
          <div className="pagerActions">
            <Button
              size="sm"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
            >
              上一页
            </Button>
            <Button size="sm" disabled={rows.length < PAGE_SIZE} onClick={() => setOffset(offset + PAGE_SIZE)}>
              下一页
            </Button>
          </div>
        </div>
      </Panel>

      {detail && (
        <Drawer title="请求详情" subtitle={detail.request_id} onClose={() => setDetail(null)}>
          <LogDetail v={detail} />
        </Drawer>
      )}
    </>
  )
}
