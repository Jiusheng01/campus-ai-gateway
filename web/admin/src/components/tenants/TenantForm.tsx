import {useState} from 'react'
import type {Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

export type TenantDraft = Partial<Tenant> & {id?: string}

export function TenantForm({value, onSave, onCancel}: {value: TenantDraft; onSave: (v: TenantDraft) => void; onCancel: () => void}) {
  const [v, setV] = useState<TenantDraft>(value)
  const isNew = !value.id
  const set = (k: keyof TenantDraft, n: number) => setV({...v, [k]: n})

  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSave(v)
      }}
    >
      <div className="formGrid">
        <Field label="租户编码" hint="业务侧识别用的短标识，例如 cs-dept-ai。">
          <input className="mono" value={v.code || ''} onChange={e => setV({...v, code: e.target.value})} />
        </Field>
        <Field label="租户名称">
          <input value={v.name || ''} onChange={e => setV({...v, name: e.target.value})} />
        </Field>
      </div>

      <div className="formGrid">
        <Field label="每日 Token 上限" hint="0 表示不限。">
          <input type="number" min="0" value={v.daily_token_limit || 0} onChange={e => set('daily_token_limit', +e.target.value)} />
        </Field>
        <Field label="每月 Token 上限" hint="0 表示不限。">
          <input type="number" min="0" value={v.monthly_token_limit || 0} onChange={e => set('monthly_token_limit', +e.target.value)} />
        </Field>
        <Field label="每月请求上限" hint="0 表示不限。">
          <input type="number" min="0" value={v.monthly_request_limit || 0} onChange={e => set('monthly_request_limit', +e.target.value)} />
        </Field>
        <Field label="每月预算（micros）" hint="0 表示不限。">
          <input type="number" min="0" value={v.monthly_budget_micros || 0} onChange={e => set('monthly_budget_micros', +e.target.value)} />
        </Field>
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
          {isNew ? '创建租户' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
