import {useState} from 'react'
import type {ApiKey, ModelConfig, Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {useConfirm} from '../ui/ConfirmDialog'

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
  const confirm = useConfirm()
  const [v, setV] = useState<KeyDraft>(() => ({...value, tenant_id: value.tenant_id || tenants[0]?.id || ''}))
  const isNew = !value.id
  const allowed = v.allowed_models || []
  // "*" is the only way to grant every model. It is a checkbox of its own
  // rather than "tick them all" so that a model added later is not silently
  // outside a key that was meant to have everything.
  const allModels = allowed.includes('*')

  // Turning a wildcard into a white list removes access to every model the
  // operator does not then tick, including models added later. That is a
  // deliberate restriction, not a silent edit.
  const changeAllModels = async (on: boolean) => {
    if (!on && allModels) {
      const ok = await confirm({
        title: '将通配符 Key 转为显式白名单？',
        description: `转换后未勾选的 ${models.length} 个模型将立即失去访问权限；以后新增的模型也不会自动允许。`,
        confirmText: '确认转换',
        danger: true,
      })
      if (!ok) return
    }
    setV({...v, allowed_models: on ? ['*'] : []})
  }
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

      <Field
        label="允许模型"
        hint={allModels ? '当前允许全部对外模型，包括以后新增的。' : '不勾选则不允许调用任何对外模型。'}
      >
        <div className="checks">
          <label>
            <input type="checkbox" checked={allModels} onChange={e => void changeAllModels(e.target.checked)} />
            全部对外模型（含以后新增）
          </label>
          {models.map(m => (
            <label key={m.id}>
              <input
                type="checkbox"
                checked={allowed.includes(m.alias)}
                disabled={allModels}
                onChange={e => toggleModel(m.alias, e.target.checked)}
              />
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
