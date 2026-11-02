import {useMemo, useState} from 'react'
import {Copy, Database} from 'lucide-react'
import {api, type AdminAudit} from '../api'
import {Badge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Drawer} from '../components/ui/Drawer'
import {Detail} from '../components/ui/Detail'
import {useToast} from '../components/ui/Toast'
import {FilterField, RECORD_WINDOW_LIMIT, RecordFilters, RecordList, RecordWindowNote} from '../components/logs/ComplianceLayout'
import {useResource} from '../lib/useResource'
import {auditLabel, auditTone} from '../lib/labels'
import {copyText, shortId, when} from '../lib/format'

/**
 * 审计日志.
 *
 * A Compliance Record, not a Request Record: it says which administrator
 * changed which control-plane object and what the gateway answered, and it
 * never carries model input or output. The shared layout with 安全事件 is
 * only the list chrome; each page keeps its own fields and its own endpoint.
 */
export function AuditPage() {
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: AdminAudit[]}>(`/admin/admin-audit?limit=${RECORD_WINDOW_LIMIT}`)
    return d.data || []
  })
  const rows = data || []
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('')
  const [detail, setDetail] = useState<AdminAudit | null>(null)

  const statuses = useMemo(() => [...new Set(rows.map(r => r.status_code))].sort((a, b) => a - b), [rows])
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter(r => {
      if (status && String(r.status_code) !== status) return false
      if (!q) return true
      return [r.actor, r.resource, r.request_id, r.action]
        .some(v => (v || '').toLowerCase().includes(q))
    })
  }, [rows, search, status])
  const narrowed = search.trim() !== '' || status !== ''

  return (
    <>
      <RecordList
        title="审计日志"
        sub="管理员操作与登录事件，不含模型调用内容。"
        filters={
          <RecordFilters
            search={search}
            onSearch={setSearch}
            searchPlaceholder="搜索管理员、资源或 Request ID..."
            onReset={() => {
              setSearch('')
              setStatus('')
            }}
          >
            <FilterField label="状态">
              <select className="filterSelect" value={status} onChange={e => setStatus(e.target.value)}>
                <option value="">全部状态</option>
                {statuses.map(code => (
                  <option key={code} value={String(code)}>{code}</option>
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
            <SkeletonRows rows={8} columns={7} />
          ) : (
            <EmptyState icon={<Database />} title="暂无审计日志记录" description="管理员执行修改操作后会在此留下记录。" />
          )
        }
        emptyFiltered={
          <EmptyState icon={<Database />} title="没有符合条件的记录" description="调整搜索词或状态后重试；筛选只覆盖已读取的最近记录。" />
        }
        note={data && !error ? <RecordWindowNote loaded={rows.length} filtered={filtered.length} narrowed={narrowed} /> : null}
      >
        <Table heads={['时间', '管理员', '操作', '资源', '状态', '来源 IP', 'Request ID']} minWidth={1040}>
          {filtered.map(a => (
            <tr key={a.id} className="clickRow" onClick={() => setDetail(a)}>
              <td>{when(a.created_at)}</td>
              <td>
                <b>{a.actor}</b>
              </td>
              <td>
                <Badge tone={auditTone(a.action)}>{auditLabel(a.action)}</Badge>
              </td>
              <td className="mono wrap" title={a.resource}>{a.resource}</td>
              <td className="numeric">{a.status_code}</td>
              <td>{a.client_ip || '—'}</td>
              <td className="mono" title={a.request_id}>{shortId(a.request_id)}</td>
            </tr>
          ))}
        </Table>
      </RecordList>

      {detail && (
        <Drawer title="审计记录详情" subtitle={auditLabel(detail.action)} onClose={() => setDetail(null)}>
          <AuditDetail record={detail} />
        </Drawer>
      )}
    </>
  )
}

function AuditDetail({record}: {record: AdminAudit}) {
  const toast = useToast()
  const copy = async () => {
    if (await copyText(record.request_id)) toast.success('Request ID 已复制')
    else toast.error('复制失败，请手动选择并复制 Request ID')
  }
  return (
    <div className="detailStack">
      <Detail
        title="变更"
        rows={[
          ['操作', <Badge key="a" tone={auditTone(record.action)}>{auditLabel(record.action)}</Badge>],
          ['管理员', record.actor],
          ['资源', <span key="r" className="mono wrap">{record.resource || '—'}</span>],
          ['结果状态', `${record.status_code}`],
          ['时间', when(record.created_at)],
          ['来源 IP', record.client_ip || '—'],
        ]}
      />
      <Detail
        title="关联请求"
        rows={[
          [
            'Request ID',
            <span key="rid" className="requestIdRow">
              <span className="mono wrap">{record.request_id || '—'}</span>
              {record.request_id && (
                <button
                  type="button"
                  className="tableActionLink"
                  title={`复制 ${record.request_id}`}
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
        这条记录描述控制面操作本身。管理操作不经过模型调用链，因此不会出现在「调用日志」里。
      </p>
    </div>
  )
}
