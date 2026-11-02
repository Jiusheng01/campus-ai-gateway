import {useMemo, useState} from 'react'
import {Copy, ShieldAlert} from 'lucide-react'
import {api, type SecurityEvent} from '../api'
import {Badge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Drawer} from '../components/ui/Drawer'
import {Detail} from '../components/ui/Detail'
import {useToast} from '../components/ui/Toast'
import {FilterField, RECORD_WINDOW_LIMIT, RecordFilters, RecordList, RecordWindowNote} from '../components/logs/ComplianceLayout'
import {useResource} from '../lib/useResource'
import {securityLabel, securityTone} from '../lib/labels'
import {copyText, when} from '../lib/format'

/**
 * 安全事件.
 *
 * These are data-plane refusals — an authentication failure, a permission or
 * quota denial — so unlike the audit log they name a tenant and a key. They
 * still carry no prompt or completion: the row says why a request was
 * refused, not what it asked.
 */
export function SecurityPage() {
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: SecurityEvent[]}>(`/admin/security-events?limit=${RECORD_WINDOW_LIMIT}`)
    return d.data || []
  })
  const rows = data || []
  const [detail, setDetail] = useState<SecurityEvent | null>(null)
  const [search, setSearch] = useState('')
  const [type, setType] = useState('')

  const types = useMemo(() => [...new Set(rows.map(e => e.event_type))].sort(), [rows])
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter(e => {
      if (type && e.event_type !== type) return false
      if (!q) return true
      return [e.tenant_id, e.api_key_id, e.client_ip, e.requested_model, e.detail, e.request_id]
        .some(v => (v || '').toLowerCase().includes(q))
    })
  }, [rows, search, type])
  const narrowed = search.trim() !== '' || type !== ''

  return (
    <>
      <RecordList
        title="安全事件"
        sub="鉴权、权限、限流与配额拒绝事件，不含请求内容。"
        filters={
          <RecordFilters
            search={search}
            onSearch={setSearch}
            searchPlaceholder="搜索租户、密钥、IP 或模型..."
            onReset={() => {
              setSearch('')
              setType('')
            }}
          >
            <FilterField label="事件类型">
              <select className="filterSelect" value={type} onChange={e => setType(e.target.value)}>
                <option value="">全部类型</option>
                {types.map(t => (
                  <option key={t} value={t}>{securityLabel(t)}</option>
                ))}
              </select>
            </FilterField>
          </RecordFilters>
        }
        source={error ? 'error' : loading && !data ? 'loading' : rows.length ? 'ready' : 'empty'}
        isFilteredEmpty={filtered.length === 0}
        emptySource={
          error ? (
            <ErrorState message={error} status={errorStatus} onRetry={reload} />
          ) : loading && !data ? (
            <SkeletonRows rows={8} columns={6} />
          ) : (
            <EmptyState icon={<ShieldAlert />} title="暂无安全事件" description="发生鉴权失败或限流拒绝后，事件会记录在这里。" />
          )
        }
        emptyFiltered={
          <EmptyState icon={<ShieldAlert />} title="没有符合条件的事件" description="调整搜索词或事件类型后重试；筛选只覆盖已读取的最近记录。" />
        }
        note={data && !error ? <RecordWindowNote loaded={rows.length} filtered={filtered.length} narrowed={narrowed} /> : null}
      >
        <Table heads={['时间', '事件', '租户', 'API 密钥', '来源 IP', '模型', '详情']} minWidth={1120}>
          {filtered.map(e => (
            <tr key={e.id} className="clickRow" onClick={() => setDetail(e)}>
              <td>{when(e.created_at)}</td>
              <td>
                <Badge tone={securityTone(e.event_type)}>{securityLabel(e.event_type)}</Badge>
              </td>
              <td className="mono">{e.tenant_id || '—'}</td>
              <td className="mono">{e.api_key_id || '—'}</td>
              <td>{e.client_ip || '—'}</td>
              <td>{e.requested_model || '—'}</td>
              <td className="cut" title={e.detail}>
                {e.detail || '—'}
              </td>
            </tr>
          ))}
        </Table>
      </RecordList>

      {detail && (
        <Drawer title="安全事件详情" subtitle={securityLabel(detail.event_type)} onClose={() => setDetail(null)}>
          <SecurityDetail event={detail} />
        </Drawer>
      )}
    </>
  )
}

function SecurityDetail({event}: {event: SecurityEvent}) {
  const toast = useToast()
  const copy = async () => {
    if (await copyText(event.request_id)) toast.success('Request ID 已复制')
    else toast.error('复制失败，请手动选择并复制 Request ID')
  }
  return (
    <div className="detailStack">
      <Detail
        title="事件"
        rows={[
          ['事件类型', <Badge key="t" tone={securityTone(event.event_type)}>{securityLabel(event.event_type)}</Badge>],
          ['时间', when(event.created_at)],
          ['详情', event.detail || '—'],
        ]}
      />
      <Detail
        title="来源"
        rows={[
          ['租户', <span key="t" className="mono wrap">{event.tenant_id || '—'}</span>],
          ['API 密钥', <span key="k" className="mono wrap">{event.api_key_id || '—'}</span>],
          ['来源 IP', event.client_ip || '—'],
          ['请求模型', event.requested_model || '—'],
        ]}
      />
      <Detail
        title="关联请求"
        rows={[
          [
            'Request ID',
            <span key="rid" className="requestIdRow">
              <span className="mono wrap">{event.request_id || '—'}</span>
              {event.request_id && (
                <button
                  type="button"
                  className="tableActionLink"
                  title={`复制 ${event.request_id}`}
                  aria-label="复制 Request ID"
                  onClick={() => void copy()}
                >
                  <Copy aria-hidden />
                  复制
                </button>
              )}
            </span>,
          ],
        ]}
      />
      <p className="hint">
        事件记录请求被拒绝的原因。到「调用日志」按 Request ID 检索可以看这次请求是否留下了调用记录；被鉴权或配额拒绝的请求可能根本没有到达上游。
      </p>
    </div>
  )
}
