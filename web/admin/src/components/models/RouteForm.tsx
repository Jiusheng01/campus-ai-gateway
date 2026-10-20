import {useState} from 'react'
import {Download, Route} from 'lucide-react'
import {listProviderUpstreamModels, type ModelConfig, type ModelRoute, type Provider} from '../../api'
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
  const [upstreams, setUpstreams] = useState<string[]>([])
  const [fetching, setFetching] = useState(false)
  const [fetchError, setFetchError] = useState('')
  const isNew = !value.id
  const providerID = v.provider_id || providers[0]?.id || ''
  const listID = `upstream-models-${model.id}`

  const loadUpstreams = async () => {
    if (!providerID) return
    setFetching(true)
    setFetchError('')
    try {
      setUpstreams(await listProviderUpstreamModels(providerID))
    } catch (e) {
      setUpstreams([])
      setFetchError(e instanceof Error ? e.message : '无法从渠道获取模型列表，仍可手动填写')
    } finally {
      setFetching(false)
    }
  }

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
        业务系统仍然调用 <b>{model.alias}</b>，这里只配置实际的上游线路。优先级相同的线路按权重分流，只有整组都失败后才会切到下一组。
      </div>

      <Field label="渠道">
        <select
          value={providerID}
          onChange={e => {
            // A list fetched from the previous channel would offer names this
            // one may not serve, so it is dropped rather than left on screen.
            setUpstreams([])
            setFetchError('')
            setV({...v, provider_id: e.target.value})
          }}
        >
          {providers.map(p => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </Field>

      <Field label="上游模型" hint="该渠道实际调用的模型名称。可以从渠道获取后选择，也可以直接填写。">
        <div className="inputWithAction">
          <input
            className="mono"
            list={listID}
            value={v.upstream_model || ''}
            onChange={e => setV({...v, upstream_model: e.target.value})}
            placeholder="例如 qwen-plus"
            autoComplete="off"
          />
          <Button
            size="sm"
            icon={<Download />}
            loading={fetching}
            disabled={!providerID}
            onClick={() => void loadUpstreams()}
            title="调用该渠道的 /models 接口，读取可用模型名称"
          >
            从渠道获取
          </Button>
          <datalist id={listID}>
            {upstreams.map(id => (
              <option key={id} value={id} />
            ))}
          </datalist>
        </div>
        {fetchError ? (
          <small className="fieldHint error">{fetchError}</small>
        ) : upstreams.length > 0 ? (
          <small className="fieldHint">已获取 {upstreams.length} 个模型，可在输入框下拉选择或继续手填。</small>
        ) : null}
      </Field>

      <div className="formGrid">
        <Field label="优先级" hint="数字越小越先使用。相同优先级为一个分组，组内按权重分流。">
          <input type="number" min="1" value={v.priority ?? 1} onChange={e => setV({...v, priority: +e.target.value})} />
        </Field>
        <Field label="流量权重" hint="同一优先级内生效；0 表示不参与分流。">
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
