import {useCallback, useState} from 'react'
import {Route} from 'lucide-react'
import {api, type RouteView} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Modal} from '../components/ui/Modal'
import {RouteEditor} from '../components/models/RouteEditor'
import {useResource} from '../lib/useResource'

/**
 * Aggregate route policy read. The editor is added by the route-policy ticket;
 * this ticket deliberately makes no write affordance.
 */
export function RoutesPage() {
  const loader = useCallback(async () => {
    const d = await api<{data: RouteView[]}>('/admin/routes')
    return d.data || []
  }, [])
  const {data, loading, error, errorStatus, reload} = useResource(loader)
  const [editing, setEditing] = useState<{id: string; alias: string} | null>(null)
  const routes = data || []

  return (
    <>
    <Panel
      title="路由策略"
      sub="所有对外模型的线路聚合视图。优先级数字越小越先尝试；同层权重只表示配置目标，不代表实际成功率。"
    >
      {loading && !data ? (
        <SkeletonRows rows={5} columns={9} />
      ) : error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : routes.length ? (
        <Table heads={['对外模型', '渠道', '上游模型', '优先级', '权重', '超时', '状态', '凭据', '配置版本', '操作']} minWidth={1240}>
          {routes.map(r => (
            <tr key={r.id}>
              <td>
                <b className="mono">{r.model_alias}</b>
                {r.model_display_name ? <div className="muted">{r.model_display_name}</div> : null}
              </td>
              <td>{r.provider_name}</td>
              <td className="mono">{r.upstream_model}</td>
              <td className="numeric">{r.priority}</td>
              <td className="numeric">{r.weight === 0 ? '0（兼容值，按 1 调度）' : r.weight}</td>
              <td className="numeric">{r.timeout_ms} ms</td>
              <td>
                <StatusBadge status={r.status} />
                {r.provider_status !== 1 ? <Badge tone="warning">渠道停用</Badge> : null}
              </td>
              <td>{r.credential_configured ? '已配置' : <Badge tone="warning">未配置</Badge>}</td>
              <td className="numeric mono">{r.config_version}</td>
              <td>
                <Button size="sm" onClick={() => setEditing({id: r.model_id, alias: r.model_alias})}>编辑该模型</Button>
              </td>
            </tr>
          ))}
        </Table>
      ) : (
        <EmptyState icon={<Route />} title="暂无线路" description="发布对外模型后，这里会显示它的线路。" />
      )}
    </Panel>
    {editing && (
      <Modal title={`编辑线路：${editing.alias}`} subtitle="整组保存，带配置版本校验。" onClose={() => setEditing(null)}>
        <RouteEditor modelID={editing.id} onSaved={reload} />
      </Modal>
    )}
    </>
  )
}
