import {useCallback, useState} from 'react'
import {Boxes, Plus, RefreshCw, Trash2} from 'lucide-react'
import {api, type ProviderModel} from '../../api'
import {Button} from '../ui/Button'
import {Badge} from '../ui/Badge'
import {EmptyState, ErrorState} from '../ui/EmptyState'
import {SkeletonRows} from '../ui/Skeleton'
import {Table} from '../ui/Table'
import {Field, Notice} from '../ui/Field'
import {useConfirm} from '../ui/ConfirmDialog'
import {useToast} from '../ui/Toast'
import {useResource} from '../../lib/useResource'
import {PublishModelModal} from './PublishModelModal'

const sourceLabel: Record<string, string> = {
  preset: '模板预置',
  fetched: '上游获取',
  manual: '手动添加',
}

/**
 * The channel's candidate list. It is a list of names the channel can serve,
 * not a publication and not an authorization: removing a row never deletes a
 * Route, which is the runtime binding authority.
 */
export function ProviderModelsPanel({providerID, onChanged}: {providerID: string; onChanged?: () => void}) {
  const toast = useToast()
  const confirm = useConfirm()
  const [name, setName] = useState('')
  const [busy, setBusy] = useState('')
  const [publishing, setPublishing] = useState<ProviderModel | null>(null)
  const loader = useCallback(async () => {
    const d = await api<{data: ProviderModel[]}>(`/admin/providers/${providerID}/models`)
    return d.data || []
  }, [providerID])
  const {data, loading, error, errorStatus, reload} = useResource(loader, [providerID])

  const refresh = async () => {
    setBusy('refresh')
    try {
      const res = await api<{data: ProviderModel[]; added: number}>(`/admin/providers/${providerID}/models/refresh`, {method: 'POST'})
      toast.success(res.added ? `已从上游新增 ${res.added} 个模型` : '候选模型已是最新')
      reload()
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '从上游刷新失败')
    } finally {
      setBusy('')
    }
  }

  const add = async () => {
    if (!name.trim()) return
    setBusy('add')
    try {
      await api(`/admin/providers/${providerID}/models`, {
        method: 'POST',
        body: JSON.stringify({upstream_model: name.trim()}),
      })
      toast.success('候选模型已保存')
      setName('')
      reload()
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '添加候选模型失败')
    } finally {
      setBusy('')
    }
  }

  const remove = async (m: ProviderModel) => {
    const links = m.links || []
    const ok = await confirm({
      title: `从候选列表移除「${m.upstream_model}」？`,
      description: links.length
        ? `该名称仍被 ${links.length} 条线路引用（${links.map(l => l.alias).join('、')}）。移除候选不会删除线路，也不会让线上模型下线。`
        : '该名称目前没有关联线路。移除候选不会影响已发布模型。',
      confirmText: '移除候选',
      danger: true,
    })
    if (!ok) return
    setBusy(m.id)
    try {
      await api(`/admin/providers/${providerID}/models/${m.id}`, {method: 'DELETE'})
      toast.success('候选模型已移除，线路未改动')
      reload()
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '移除候选模型失败')
    } finally {
      setBusy('')
    }
  }

  const items = data || []

  return (
    <div className="drawerStack">
      <Notice>
        候选模型描述“这个渠道能提供什么”，不是发布，也不是授权。刷新只新增，不删除已有名称，不覆盖来源。
      </Notice>

      <div className="quickActions">
        <Button icon={<RefreshCw />} loading={busy === 'refresh'} disabled={!!busy} onClick={() => void refresh()}>
          从上游刷新
        </Button>
      </div>

      <div className="form">
        <Field label="手动添加上游模型" hint="支持厂商的自定义模型 ID 或推理接入点 ID（例如 ep-…）。已存在的名称会保留原来源。">
          <div className="inlineForm">
            <input className="mono" value={name} onChange={e => setName(e.target.value)} placeholder="模型 ID / endpoint ID" />
            <Button variant="primary" icon={<Plus />} loading={busy === 'add'} disabled={!name.trim() || !!busy} onClick={() => void add()}>
              添加
            </Button>
          </div>
        </Field>
      </div>

      {loading && !data ? (
        <SkeletonRows rows={4} columns={5} />
      ) : error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : items.length ? (
        <Table heads={['上游模型', '来源', '状态', '关联对外模型', '操作']} minWidth={700}>
          {items.map(m => (
            <tr key={m.id}>
              <td className="mono">{m.upstream_model}</td>
              <td><Badge>{sourceLabel[m.source] || m.source}</Badge></td>
              <td>{m.status === 1 ? '可用' : '停用'}</td>
              <td>
                {m.links?.length
                  ? m.links.map(l => (
                      <div key={l.route_id}>
                        <span className="mono">{l.alias}</span>
                        <span className="muted"> · 线路 {l.route_status === 1 ? '启用' : '停用'}</span>
                      </div>
                    ))
                  : <span className="muted">尚未发布</span>}
              </td>
              <td className="rowActions">
                <button className="link" disabled={!!busy} onClick={() => setPublishing(m)}>
                  <Plus aria-hidden />
                  发布为对外模型
                </button>
                <button className="link dangerText" disabled={!!busy} onClick={() => void remove(m)}>
                  <Trash2 aria-hidden />
                  移除候选
                </button>
              </td>
            </tr>
          ))}
        </Table>
      ) : (
        <EmptyState
          icon={<Boxes />}
          title="暂无候选模型"
          description="从上游刷新，或手动添加渠道实际提供的模型 ID。"
        />
      )}

      {publishing && (
        <PublishModelModal
          candidate={publishing}
          onClose={() => setPublishing(null)}
          onPublished={() => {
            reload()
            onChanged?.()
          }}
        />
      )}
    </div>
  )
}
