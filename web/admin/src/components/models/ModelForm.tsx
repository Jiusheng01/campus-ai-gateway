import {useState} from 'react'
import {Route} from 'lucide-react'
import type {ModelConfig, Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

export type ModelDraft = Partial<ModelConfig> & {id?: string}

export function ModelForm({
  value,
  providers,
  onSave,
  onCancel,
}: {
  value: ModelDraft
  providers: Provider[]
  onSave: (v: ModelDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<ModelDraft>(() => ({
    ...value,
    provider_id: value.provider_id || providers[0]?.id || '',
  }))
  const isNew = !value.id

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSave(v)
      }}
    >
      <Field label="对外模型名称" hint="业务系统调用 /v1/* API 时使用的 model 名称，例如 campus-smart。">
        <input className="mono" value={v.alias || ''} onChange={e => setV({...v, alias: e.target.value})} />
      </Field>

      <Field label="主供应商" hint="默认使用的模型供应商。">
        <select value={v.provider_id || providers[0]?.id || ''} onChange={e => setV({...v, provider_id: e.target.value})}>
          {providers.map(p => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </Field>

      <Field label="主线路模型" hint="该供应商实际调用的上游模型名称。">
        <input className="mono" value={v.upstream_model || ''} onChange={e => setV({...v, upstream_model: e.target.value})} />
      </Field>

      <div className="notice info">
        <Route aria-hidden />
        这里配置的是<b>初始主线路</b>。保存后可继续添加备用线路，用于故障切换。
      </div>

      <Field label="状态">
        <select value={v.status || 1} onChange={e => setV({...v, status: +e.target.value})}>
          <option value="1">启用</option>
          <option value="2">停用</option>
        </select>
      </Field>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit">
          {isNew ? '创建模型' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
