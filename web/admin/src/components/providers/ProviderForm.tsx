import {useState} from 'react'
import {Info, Plus} from 'lucide-react'
import type {Provider} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {PasswordInput} from '../ui/PasswordInput'
import {PROTOCOLS, providerTemplates, protocolLabel} from './templates'

export type ProviderDraft = Partial<Provider> & {id?: string}

type CredentialMode = 'inline' | 'env'

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
  // A new channel starts in inline mode: the operator pastes the vendor key.
  // An existing env-sourced channel starts in env mode so the reference it
  // actually uses is visible rather than hidden behind a blank password field.
  const [mode, setMode] = useState<CredentialMode>(
    value.credential?.source_type === 'env' || (!value.credential && value.credential_env)
      ? 'env'
      : 'inline',
  )
  const [formError, setFormError] = useState('')
  const isNew = !value.id
  const credential = value.credential
  const inlineConfigured = credential?.source_type === 'inline' && credential.configured
  const envConfigured = credential?.source_type === 'env' && credential.configured

  const applyTemplate = (id: string) => {
    setTemplateId(id)
    const t = providerTemplates.find(x => x.id === id)
    if (!t) return
    // Templates only prefill defaults; the custom template's defaults are blank
    // so the operator starts from an empty form rather than stale values. The
    // env-var name stays in the draft even in inline mode, so switching to env
    // mode later still finds the template's conventional name.
    setV(prev => ({...prev, ...t.values}))
  }

  const submit = () => {
    const apiKey = (v.api_key || '').trim()
    const credentialEnv = (v.credential_env || '').trim()
    if (mode === 'env' && !credentialEnv) {
      setFormError('请输入凭据环境变量名。')
      return
    }
    if (isNew && mode === 'inline' && !apiKey) {
      setFormError('请粘贴厂商 API Key，或改为引用环境变量。')
      return
    }
    setFormError('')

    // Only the selected source is sent. A template's env-var name must not ride
    // along with an inline key: the API treats both sources on one write as
    // ambiguous. An empty inline key on edit means "keep the stored credential".
    const payload: ProviderDraft = {
      ...v,
      api_key: mode === 'inline' ? apiKey : undefined,
      credential_env: mode === 'env' ? credentialEnv : undefined,
    }
    delete payload.credential
    onSave(payload)
  }

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        submit()
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

      <Field label="接口协议" hint="网关只支持 OpenAI Compatible 协议；其他协议的渠道会在保存时被拒绝。">
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

      {mode === 'inline' ? (
        <Field
          label="API Key"
          hint={
            inlineConfigured
              ? '留空表示不修改已保存的 Key；填入则覆盖。'
              : '粘贴厂商签发的 API Key。保存后以密文入库，只写不读。'
          }
        >
          <PasswordInput
            value={v.api_key || ''}
            onChange={key => setV({...v, api_key: key})}
            placeholder={inlineConfigured ? `已配置（尾号 ${credential?.last4 || '****'}），留空不改动` : 'sk-…'}
            autoComplete="new-password"
          />
        </Field>
      ) : (
        <Field
          label="凭据环境变量"
          hint="网关进程环境变量的名称，例如 DASHSCOPE_API_KEY。不能填网关自身的变量（ADMIN_、DATABASE_、REDIS_ 等），否则等于把管理密钥交给这条渠道。"
        >
          <input
            className="mono"
            placeholder="DASHSCOPE_API_KEY"
            value={v.credential_env || ''}
            onChange={e => setV({...v, credential_env: e.target.value})}
          />
        </Field>
      )}

      <div className="checks">
        <label>
          <input
            type="checkbox"
            checked={mode === 'env'}
            onChange={e => {
              setMode(e.target.checked ? 'env' : 'inline')
              setFormError('')
            }}
          />
          改为引用环境变量
        </label>
      </div>

      {credential?.source_type === 'env' && (
        <div className="notice info">
          <Info aria-hidden />
          <span>
            当前引用 <span className="mono">{v.credential_env || '—'}</span>
            {envConfigured
              ? `（已读取到值，尾号 ${credential.last4}）`
              : '（当前进程环境中未读取到值）'}
            。
          </span>
        </div>
      )}

      {inlineConfigured && (
        <div className="notice info">
          <Info aria-hidden />
          当前已保存内联密钥
          {credential.last4 ? `（尾号 ${credential.last4}）` : ''}
          ；内容不会回显。
        </div>
      )}

      {formError && (
        <div className="notice warning" role="alert">
          {formError}
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
        <Button variant="primary" type="submit" icon={isNew ? <Plus /> : undefined}>
          {isNew ? '创建渠道' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
