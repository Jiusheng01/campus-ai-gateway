import {useState} from 'react'
import {Route} from 'lucide-react'
import type {ModelConfig, ModelRoute, Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

export type RouteDraft = Partial<ModelRoute> & {id?: string}

export function RouteForm({
  value,
  model,
  providers,
  onSave,
  onCancel,
}: {
  value: RouteDraft
  model: ModelConfig
  providers: Provider[]
  onSave: (m: ModelConfig, v: RouteDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<RouteDraft>(() => ({
    ...value,
    provider_id: value.provider_id || providers[0]?.id || '',
  }))
  const isNew = !value.id

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSave(model, v)
      }}
    >
      <div className="notice info">
        <Route aria-hidden />
        业务系统仍然调用 <b>{model.alias}</b>，这里只配置实际的上游线路。
      </div>

      <Field label="渠道">
        <select value={v.provider_id || providers[0]?.id || ''} onChange={e => setV({...v, provider_id: e.target.value})}>
          {providers.map(p => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </Field>

      <Field label="上游模型" hint="该渠道实际调用的模型名称。">
        <input className="mono" value={v.upstream_model || ''} onChange={e => setV({...v, upstream_model: e.target.value})} />
      </Field>

      <div className="formGrid">
        <Field label="优先级" hint="数字越小越优先，用于决定主线路。">
          <input type="number" min="1" value={v.priority ?? 1} onChange={e => setV({...v, priority: +e.target.value})} />
        </Field>
        <Field label="流量权重">
          <input type="number" min="0" value={v.weight ?? 100} onChange={e => setV({...v, weight: +e.target.value})} />
        </Field>
        <Field label="超时时间（ms）">
          <input type="number" min="1" value={v.timeout_ms ?? 60000} onChange={e => setV({...v, timeout_ms: +e.target.value})} />
        </Field>
      </div>

      <Field label="状态">
        <select value={v.status ?? 1} onChange={e => setV({...v, status: +e.target.value})}>
          <option value="1">启用</option>
          <option value="2">停用</option>
        </select>
      </Field>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit">
          {isNew ? '添加线路' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
