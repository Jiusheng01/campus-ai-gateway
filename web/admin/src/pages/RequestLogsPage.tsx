import {useCallback, useState} from 'react'
import {Copy, Download, FileClock, Search, X} from 'lucide-react'
import {api, download, type RequestLog} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, HttpStatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {LogDetail} from '../components/logs/LogDetail'
import {useResource} from '../lib/useResource'
import {copyText, ms, num, shortId, when} from '../lib/format'

const PAGE_SIZE = 50

type Filters = {model: string; provider: string; status: string; request_id: string; failover: string}

const emptyFilters: Filters = {model: '', provider: '', status: '', request_id: '', failover: ''}

export function RequestLogsPage() {
  const toast = useToast()
  const [filters, setFilters] = useState<Filters>(emptyFilters)
  // `applied` is what the request actually used, so typing in a filter box does
  // not refetch on every keystroke.
  const [applied, setApplied] = useState<Filters>(emptyFilters)
  const [offset, setOffset] = useState(0)
  const [detail, setDetail] = useState<RequestLog | null>(null)

  const loader = useCallback(async () => {
    const q = new URLSearchParams({limit: String(PAGE_SIZE), offset: String(offset)})
    Object.entries(applied).forEach(([k, v]) => v && q.set(k, v))
    const d = await api<{data: RequestLog[]}>(`/admin/request-logs?${q}`)
    return d.data || []
  }, [applied, offset])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [applied, offset])
  const rows = data || []

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
    const q = new URLSearchParams(Object.entries(applied).filter(([, v]) => v) as [string, string][])
    try {
      await download(`/admin/request-logs/export?${q}`, 'campus-gateway-request-logs.csv')
      toast.success('请求日志已导出')
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

  return (
    <>
      <Panel
        title="请求日志"
        sub="一次业务请求对应一行。点击任意一行查看路由、用量与时延详情。"
        actions={
          <Button icon={<Download />} onClick={() => void exportCsv()}>
            导出 CSV
          </Button>
        }
      >
        <div className="filters">
          <input placeholder="模型" value={filters.model} onChange={f('model')} />
          <input placeholder="供应商" value={filters.provider} onChange={f('provider')} />
          <input placeholder="HTTP 状态" value={filters.status} onChange={f('status')} />
          <input placeholder="Request ID" value={filters.request_id} onChange={f('request_id')} />
          <select value={filters.failover} onChange={f('failover')}>
            <option value="">全部故障切换</option>
            <option value="true">发生故障切换</option>
            <option value="false">未发生故障切换</option>
          </select>
          <div className="filterActions">
            <Button variant="primary" icon={<Search />} onClick={search}>
              查询
            </Button>
            <Button icon={<X />} onClick={reset}>
              重置
            </Button>
          </div>
        </div>

        {loading && !data ? (
          <SkeletonRows rows={8} columns={8} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : rows.length ? (
          <Table heads={['时间', '状态', '模型', '供应商', 'Tokens', '延迟', '故障切换', 'Request ID']} minWidth={1040}>
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
