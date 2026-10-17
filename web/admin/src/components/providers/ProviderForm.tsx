import {useState} from 'react'
import {Info, Plus} from 'lucide-react'
import type {Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {PROTOCOLS, providerTemplates, protocolLabel} from './templates'

export type ProviderDraft = Partial<Provider> & {id?: string}

export function ProviderForm({
  value,
  onSave,
  onCancel,
}: {
  value: ProviderDraft
  onSave: (v: ProviderDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<ProviderDraft>(value)
  const [templateId, setTemplateId] = useState('custom')
  const isNew = !value.id

  const applyTemplate = (id: string) => {
    setTemplateId(id)
    const t = providerTemplates.find(x => x.id === id)
    if (!t) return
    // Templates only prefill defaults; the custom template's defaults are blank
    // so the operator starts from an empty form rather than stale values.
    setV(prev => ({...prev, ...t.values}))
  }

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSave(v)
      }}
    >
      {/* Editing an existing supplier must never silently overwrite its
          configuration, so the template picker only appears on create. */}
      {isNew && (
        <Field label="渠道模板" hint="模板只用于预填默认值，选择后所有字段仍可手工修改。">
          <select value={templateId} onChange={e => applyTemplate(e.target.value)}>
            {providerTemplates.map(t => (
              <option key={t.id} value={t.id}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>
      )}

      <Field label="渠道名称" hint="配置标识，例如 qwen、deepseek、glm。">
        <input value={v.name || ''} onChange={e => setV({...v, name: e.target.value})} />
      </Field>

      <Field label="接口协议">
        <select value={v.protocol || 'openai-chat'} onChange={e => setV({...v, protocol: e.target.value})}>
          {PROTOCOLS.map(p => (
            <option key={p} value={p}>
              {protocolLabel(p)}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Base URL">
        <input className="mono" value={v.base_url || ''} onChange={e => setV({...v, base_url: e.target.value})} />
      </Field>

      <Field label="凭据环境变量" hint="网关进程环境变量的名称，例如 DASHSCOPE_API_KEY。">
        <input className="mono" value={v.credential_env || ''} onChange={e => setV({...v, credential_env: e.target.value})} />
      </Field>

      <div className="notice info">
        <Info aria-hidden />
        这里只保存凭据环境变量名称，不保存真实厂商 API 密钥。
      </div>

      <Field label="状态">
        <select value={v.status || 1} onChange={e => setV({...v, status: +e.target.value})}>
          <option value="1">启用</option>
          <option value="2">停用</option>
        </select>
      </Field>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit" icon={isNew ? <Plus /> : undefined}>
          {isNew ? '创建渠道' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
