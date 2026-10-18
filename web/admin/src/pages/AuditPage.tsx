import {Database} from 'lucide-react'
import {api, type AdminAudit} from '../api'
import {Panel} from '../components/ui/Panel'
import {Badge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {useResource} from '../lib/useResource'
import {auditLabel, auditTone} from '../lib/labels'
import {shortId, when} from '../lib/format'

export function AuditPage() {
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: AdminAudit[]}>('/admin/admin-audit?limit=200')
    return d.data || []
  })
  const rows = data || []

  return (
    <Panel title="审计日志" sub="记录管理员对控制面发起的修改操作与登录事件，最多显示最近 200 条。">
      {loading && !data ? (
        <SkeletonRows rows={8} columns={7} />
      ) : error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : rows.length ? (
        <Table heads={['时间', '管理员', '操作', '资源', '状态', '来源 IP', 'Request ID']} minWidth={1040}>
          {rows.map(a => (
            <tr key={a.id}>
              <td>{when(a.created_at)}</td>
              <td>
                <b>{a.actor}</b>
              </td>
              <td>
                <Badge tone={auditTone(a.action)}>{auditLabel(a.action)}</Badge>
              </td>
              <td className="mono cut" title={a.resource}>
                {a.resource}
              </td>
              <td className="numeric">{a.status_code}</td>
              <td>{a.client_ip || '—'}</td>
              <td className="mono" title={a.request_id}>
                {shortId(a.request_id)}
              </td>
            </tr>
          ))}
        </Table>
      ) : (
        <EmptyState icon={<Database />} title="暂无审计日志记录" description="管理员执行修改操作后会在此留下记录。" />
      )}
    </Panel>
  )
}
