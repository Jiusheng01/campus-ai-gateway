import {useState} from 'react'
import {Route} from 'lucide-react'
import type {ModelConfig, ModelRoute, Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

export type RouteDraft = Partial<ModelRoute> & {id?: string}

/**
 * arkBase reports whether a channel's base URL is Volcengine Ark's
 * OpenAI-compatible root.
 *
 * Ark addresses an inference endpoint by its ID (`ep-...`) rather than by a
 * model name, and the base URL is the only vendor signal in the configuration.
 * This drives a hint and nothing else: the gateway cannot know every vendor's
 * naming rule, so a value that does not match is still allowed to be saved.
 */
function arkBase(baseUrl: string | undefined) {
  if (!baseUrl) return false
  try {
    return /\/api\/v3\/?$/.test(new URL(baseUrl).pathname)
  } catch {
    return false
  }
}

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
  const providerId = v.provider_id || providers[0]?.id || ''
  const selectedProvider = providers.find(p => p.id === providerId)

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
        <select value={providerId} onChange={e => setV({...v, provider_id: e.target.value})}>
          {providers.map(p => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </Field>

      <Field
        label="上游模型"
        hint={
          arkBase(selectedProvider?.base_url)
            ? '该渠道的 Base URL 指向火山方舟的 /api/v3，这里要填推理接入点 ID（ep-…），不是模型名称。'
            : '该渠道实际调用的上游模型名称。'
        }
      >
        <input className="mono" value={v.upstream_model || ''} onChange={e => setV({...v, upstream_model: e.target.value})} />
      </Field>

      {/* Failover tuning is a second question. The defaults (primary band, equal
          weight, 60 s) are what an operator opening a model needs; changing
          them is a deliberate act, so the controls stay collapsed until asked
          for rather than filling the first screen. */}
      <details className="formAdvanced">
        <summary>高级：优先级 / 流量权重 / 超时</summary>
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
      </details>

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
