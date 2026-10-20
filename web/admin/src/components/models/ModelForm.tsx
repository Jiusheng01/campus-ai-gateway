import {useState} from 'react'
import {Route} from 'lucide-react'
import type {ModelConfig, Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

/**
 * ModelDraft is a model write.
 *
 * The model itself has no channel: its channel and upstream model belong to its
 * lines (Routes). On create the draft also carries the channel and upstream
 * model of the model's first line, because a model with no line could never
 * serve traffic. On edit those fields are absent — editing a model changes its
 * name and status only, so it can never silently re-point a line.
 */
export type ModelDraft = Partial<ModelConfig> & {id?: string; provider_id?: string; upstream_model?: string}

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
  const [v, setV] = useState<ModelDraft>(() =>
    // Only a create carries a line binding. Adding a channel to an edit draft
    // would submit half a binding (no upstream model) and the update would be
    // rejected — and an edit must not touch a line at all.
    value.id ? {...value} : {...value, provider_id: value.provider_id || providers[0]?.id || ''},
  )
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

      {isNew ? (
        <>
          <Field label="首条线路的渠道" hint="模型创建时写入的第一条线路。保存后可在下方线路列表里增删。">
            <select value={v.provider_id || providers[0]?.id || ''} onChange={e => setV({...v, provider_id: e.target.value})}>
              {providers.map(p => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label="首条线路的上游模型" hint="该渠道实际调用的上游模型名称。">
            <input className="mono" value={v.upstream_model || ''} onChange={e => setV({...v, upstream_model: e.target.value})} />
          </Field>

          <div className="notice info">
            <Route aria-hidden />
            保存时会创建模型并写入这条<b>首条线路</b>。之后可在下方继续添加备用线路，用于故障切换。
          </div>
        </>
      ) : (
        <div className="notice info">
          <Route aria-hidden />
          渠道、上游模型、优先级、权重与超时都在下方线路列表里管理；这里只修改名称与状态，保存<b>不会改动任何线路</b>。
        </div>
      )}

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
