import {useState} from 'react'
import {ShieldAlert} from 'lucide-react'
import {api, type SecurityEvent} from '../api'
import {Panel} from '../components/ui/Panel'
import {Badge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Drawer} from '../components/ui/Drawer'
import {Detail} from '../components/ui/Detail'
import {useResource} from '../lib/useResource'
import {securityLabel, securityTone} from '../lib/labels'
import {when} from '../lib/format'

export function SecurityPage() {
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: SecurityEvent[]}>('/admin/security-events?limit=200')
    return d.data || []
  })
  const [detail, setDetail] = useState<SecurityEvent | null>(null)
  const rows = data || []

  return (
    <>
      <Panel title="安全事件" sub="集中展示鉴权、权限、限流与租户配额拒绝事件，最多显示最近 200 条。">
        {loading && !data ? (
          <SkeletonRows rows={8} columns={6} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : rows.length ? (
          <Table heads={['时间', '事件', '租户', 'API 密钥', '来源 IP', '模型', '详情']} minWidth={1120}>
            {rows.map(e => (
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
        ) : (
          <EmptyState icon={<ShieldAlert />} title="暂无安全事件" description="发生鉴权失败或限流拒绝后，事件会记录在这里。" />
        )}
      </Panel>

      {detail && (
        <Drawer title="安全事件详情" subtitle={securityLabel(detail.event_type)} onClose={() => setDetail(null)}>
          <div className="detailStack">
            <Detail
              title="事件"
              rows={[
                ['事件类型', <Badge key="t" tone={securityTone(detail.event_type)}>{securityLabel(detail.event_type)}</Badge>],
                ['时间', when(detail.created_at)],
                ['Request ID', <span key="r" className="mono wrap">{detail.request_id || '—'}</span>],
                ['详情', detail.detail || '—'],
              ]}
            />
            <Detail
              title="来源"
              rows={[
                ['租户', detail.tenant_id || '—'],
                ['API 密钥', detail.api_key_id || '—'],
                ['来源 IP', detail.client_ip || '—'],
                ['请求模型', detail.requested_model || '—'],
              ]}
            />
          </div>
        </Drawer>
      )}
    </>
  )
}
