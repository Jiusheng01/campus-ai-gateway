import {useState} from 'react'
import type {ApiKey, ModelConfig, Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

export type KeyDraft = Partial<ApiKey> & {id?: string}

export function KeyForm({
  value,
  tenants,
  models,
  onSave,
  onCancel,
}: {
  value: KeyDraft
  tenants: Tenant[]
  models: ModelConfig[]
  onSave: (v: KeyDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<KeyDraft>(() => ({...value, tenant_id: value.tenant_id || tenants[0]?.id || ''}))
  const isNew = !value.id
  const allowed = v.allowed_models || []

  const toggleModel = (alias: string, on: boolean) =>
    setV({...v, allowed_models: on ? [...allowed, alias] : allowed.filter(x => x !== alias)})

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSave(v)
      }}
    >
      <Field label="名称" hint="便于识别的用途说明，例如 教务系统-生产。">
        <input value={v.name || ''} onChange={e => setV({...v, name: e.target.value})} />
      </Field>

      <Field label="租户">
        <select value={v.tenant_id || tenants[0]?.id || ''} onChange={e => setV({...v, tenant_id: e.target.value})}>
          {tenants.map(t => (
            <option key={t.id} value={t.id}>
              {t.name}（{t.code}）
            </option>
          ))}
        </select>
      </Field>

      <Field label="允许模型" hint="不勾选则不允许调用任何对外模型。">
        <div className="checks">
          {models.map(m => (
            <label key={m.id}>
              <input type="checkbox" checked={allowed.includes(m.alias)} onChange={e => toggleModel(m.alias, e.target.checked)} />
              {m.alias}
            </label>
          ))}
          {!models.length && <span className="muted">暂无可用的对外模型</span>}
        </div>
      </Field>

      <Field label="允许 IP / CIDR" hint="每行一个，留空表示不限制来源地址。">
        <textarea
          rows={3}
          value={(v.allowed_ips || []).join('\n')}
          onChange={e =>
            setV({
              ...v,
              allowed_ips: e.target.value
                .split(/\n|,/)
                .map(x => x.trim())
                .filter(Boolean),
            })
          }
        />
      </Field>

      <div className="formGrid">
        <Field label="RPM 上限" hint="每分钟请求数，0 表示不限。">
          <input type="number" min="0" value={v.rpm_limit ?? 0} onChange={e => setV({...v, rpm_limit: +e.target.value})} />
        </Field>
        <Field label="TPM 上限" hint="每分钟 Token 数，0 表示不限。">
          <input type="number" min="0" value={v.tpm_limit ?? 0} onChange={e => setV({...v, tpm_limit: +e.target.value})} />
        </Field>
        <Field label="最大并发">
          <input type="number" min="0" value={v.max_concurrency ?? 0} onChange={e => setV({...v, max_concurrency: +e.target.value})} />
        </Field>
        <Field label="线路分组" hint="可选，用于把请求约束到指定线路分组。">
          <input value={v.route_group || ''} onChange={e => setV({...v, route_group: e.target.value})} />
        </Field>
      </div>

      <Field label="过期时间（ISO 8601，留空表示永不过期）">
        <input className="mono" placeholder="2026-12-31T00:00:00Z" value={v.expire_at || ''} onChange={e => setV({...v, expire_at: e.target.value || undefined})} />
      </Field>

      <Field label="状态">
        <select value={v.status || 1} onChange={e => setV({...v, status: +e.target.value})}>
          <option value="1">启用</option>
          <option value="2">停用</option>
        </select>
      </Field>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit">
          {isNew ? '创建 Key' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
