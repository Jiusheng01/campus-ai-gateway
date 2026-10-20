import {useCallback, useMemo, useState} from 'react'
import {AlertTriangle, Plus, Trash2} from 'lucide-react'
import {api, type ModelDetail, type ModelRoute, type Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field, Notice} from '../ui/Field'
import {EmptyState, ErrorState} from '../ui/EmptyState'
import {SkeletonRows} from '../ui/Skeleton'
import {useToast} from '../ui/Toast'
import {useResource} from '../../lib/useResource'

type DraftRoute = {
  id?: string
  provider_id: string
  upstream_model: string
  priority: number
  weight: number
  timeout_ms: number
  status: number
}

function toDraft(routes: ModelRoute[]): DraftRoute[] {
  return routes.map(r => ({
    id: r.id,
    provider_id: r.provider_id,
    upstream_model: r.upstream_model,
    priority: r.priority,
    weight: r.weight,
    timeout_ms: r.timeout_ms,
    status: r.status,
  }))
}

/**
 * The one route editor used by the aggregate route page and the model detail.
 * This is the advanced mode; the mode projections will sit on the same draft
 * and submit the same array.
 */
export function RouteEditor({modelID, onSaved}: {modelID: string; onSaved?: () => void}) {
  const toast = useToast()
  const loader = useCallback(async () => {
    const [detail, providers] = await Promise.all([
      api<ModelDetail>(`/admin/models/${modelID}`),
      api<{data: Provider[]}>('/admin/providers').then(d => d.data || []),
    ])
    return {detail, providers}
  }, [modelID])
  const {data, loading, error, errorStatus, reload} = useResource(loader, [modelID])
  const [draft, setDraft] = useState<DraftRoute[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<number | null>(null)
  const [formError, setFormError] = useState('')

  const routes = useMemo(() => draft ?? (data ? toDraft(data.detail.routes) : []), [draft, data])
  const version = data?.detail.version ?? 0
  const providers = data?.providers || []

  const setRow = (i: number, patch: Partial<DraftRoute>) => {
    setDraft(prev => {
      const next = (prev ?? (data ? toDraft(data.detail.routes) : [])).slice()
      next[i] = {...next[i], ...patch}
      return next
    })
  }

  const addRow = () => {
    const maxPriority = routes.reduce((m, r) => Math.max(m, r.priority), 0)
    const next = (draft ?? (data ? toDraft(data.detail.routes) : [])).slice()
    next.push({
      provider_id: providers[0]?.id || '',
      upstream_model: '',
      priority: maxPriority + 1,
      weight: 100,
      timeout_ms: 60000,
      status: 1,
    })
    setDraft(next)
  }

  const removeRow = (i: number) => {
    const next = (draft ?? (data ? toDraft(data.detail.routes) : [])).slice()
    next.splice(i, 1)
    setDraft(next)
  }

  const save = async () => {
    setBusy(true)
    setFormError('')
    setConflict(null)
    try {
      const out = await api<{version: number; routes: ModelRoute[]}>(
        `/admin/models/${modelID}/routes`,
        {
          method: 'PUT',
          body: JSON.stringify({expected_version: version, routes}),
        },
      )
      toast.success(`线路已保存，配置版本 ${out.version}`)
      setDraft(null)
      reload()
      onSaved?.()
    } catch (e) {
      const apiError = e as {status?: number; message?: string; code?: string}
      if (apiError.status === 409) {
        const match = /current[_ ]version[^0-9]*(\d+)/i.exec(apiError.message || '')
        setConflict(match ? Number(match[1]) : null)
      } else {
        setFormError(apiError.message || '保存线路失败')
      }
    } finally {
      setBusy(false)
    }
  }

  if (loading && !data) return <SkeletonRows rows={4} columns={7} />
  if (error) return <ErrorState message={error} status={errorStatus} onRetry={reload} />
  if (!data) return null

  return (
    <div className="drawerStack">
      <Notice>
        优先级数字越小越先尝试；同一优先级内按权重做平滑轮询。超时是上游响应头等待时间，不是完整回答时间。
      </Notice>
      {conflict !== null && (
        <div className="notice warning" role="alert">
          <AlertTriangle aria-hidden />
          该模型的线路已被其他管理员修改。你的草稿仍保留；当前版本为 {conflict}。
          <Button size="sm" onClick={() => { setDraft(null); setConflict(null); reload() }}>加载最新版本</Button>
        </div>
      )}
      {formError && <div className="notice warning" role="alert">{formError}</div>}

      {routes.length ? (
        <div className="routeEditor">
          {routes.map((r, i) => (
            <div className="routeEditorRow" key={r.id || `new-${i}`}>
              <Field label="渠道">
                <select value={r.provider_id} onChange={e => setRow(i, {provider_id: e.target.value})}>
                  {providers.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
                </select>
              </Field>
              <Field label="上游模型">
                <input className="mono" value={r.upstream_model} onChange={e => setRow(i, {upstream_model: e.target.value})} />
              </Field>
              <Field label="优先级">
                <input type="number" min={1} max={1000} value={r.priority} onChange={e => setRow(i, {priority: +e.target.value})} />
              </Field>
              <Field label="权重" hint={r.weight === 0 ? '兼容值，按 1 调度' : undefined}>
                <input type="number" min={0} max={10000} value={r.weight} onChange={e => setRow(i, {weight: +e.target.value})} />
              </Field>
              <Field label="超时（ms）">
                <input type="number" min={1} max={600000} value={r.timeout_ms} onChange={e => setRow(i, {timeout_ms: +e.target.value})} />
              </Field>
              <Field label="状态">
                <select value={r.status} onChange={e => setRow(i, {status: +e.target.value})}>
                  <option value="1">启用</option>
                  <option value="2">停用</option>
                </select>
              </Field>
              <Button size="sm" variant="danger" icon={<Trash2 />} onClick={() => removeRow(i)}>删除</Button>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState title="没有线路" description="至少保留一条线路，已启用的模型至少保留一条启用线路。" />
      )}

      <div className="formActions">
        <Button icon={<Plus />} onClick={addRow}>添加线路</Button>
        <Button variant="primary" loading={busy} disabled={busy || !routes.length} onClick={() => void save()}>
          保存整组线路
        </Button>
      </div>
      <p className="hint">保存会原子替换整组线路，并保留未修改线路的 route ID。配置版本 {version}。</p>
    </div>
  )
}
