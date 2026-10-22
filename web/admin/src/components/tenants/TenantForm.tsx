import {useState} from 'react'
import {Layers} from 'lucide-react'
import type {ModelConfig, Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {ModelScopePicker} from '../models/ModelScopePicker'

export type TenantDraft = Partial<Tenant> & {id?: string}

export function TenantForm({
  value,
  models = [],
  onSave,
  onCancel,
}: {
  value: TenantDraft
  models?: ModelConfig[]
  onSave: (v: TenantDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<TenantDraft>(() => ({
    ...value,
    // A create form, and a response from before this field existed, start
    // unrestricted. That is the column default and what every existing Tenant
    // already means, so opening the form never narrows access by accident.
    allowed_models: value.allowed_models ?? ['*'],
  }))
  const isNew = !value.id
  const set = (k: keyof TenantDraft, n: number) => setV({...v, [k]: n})

  // The same three shapes the API accepts: "*" (everything, including models
  // added later), a named list, and [] (nothing). The picker is shared with
  // the key forms so the three surfaces cannot drift apart.
  const allowedModels = v.allowed_models ?? []
  const allModels = allowedModels.includes('*')

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
      </div>

      {/* Rate limits are the tenant's, not the key's (ADR-0015): every key under
          this tenant shares these three. 0 means unlimited. */}
      <div className="formGrid">
        <Field label="RPM 上限" hint="每分钟请求数，本租户所有密钥共享，0 表示不限。">
          <input type="number" min="0" value={v.rpm_limit || 0} onChange={e => set('rpm_limit', +e.target.value)} />
        </Field>
        <Field label="TPM 上限" hint="每分钟 Token 数，本租户所有密钥共享，0 表示不限。">
          <input type="number" min="0" value={v.tpm_limit || 0} onChange={e => set('tpm_limit', +e.target.value)} />
        </Field>
        <Field label="最大并发" hint="本租户同时在处理的请求数，0 表示不限。">
          <input type="number" min="0" value={v.max_concurrency || 0} onChange={e => set('max_concurrency', +e.target.value)} />
        </Field>
      </div>

      {/* The Tenant allowlist is the ceiling for personal API Keys written by
          this Tenant's members. It is not a data-plane check: existing keys keep
          their own scope, so editing this value never changes live traffic. */}
      <fieldset className="formSectionFieldset">
        <legend className="formSectionLegend">
          <Layers aria-hidden /> 模型授权
        </legend>

        {/* Narrowing the list looks like a revocation and is not one. Say so
            where the operator changes it, not in a doc they would have to
            find. */}
        <p className="formSectionNote">
          此列表是成员创建或编辑个人 API Key 时可选的模型上限。缩减它不会撤销或追溯修改已签发的 API Key，
          已有 Key 继续按签发时的授权范围调用。管理员签发 Key 的授权在「调用管理」中单独维护。
        </p>

        <ModelScopePicker
          value={allowedModels}
          onChange={next => setV(prev => ({...prev, allowed_models: next}))}
          options={models.map(m => ({alias: m.alias, displayName: m.display_name}))}
          title="成员可用的对外模型"
          wildcardLabel="全部对外模型（含以后新增）"
          wildcardNoticeTitle="已启用通配符授权"
          wildcardNoticeText="该租户的成员可以调用系统内所有已启用的对外模型，以后新增的模型也会自动允许。"
          emptyPlaceholder="未选择任何模型（该租户的成员将无法签发可用模型）"
          addPlaceholder="+ 选择要添加的模型授权…"
          hint={
            allModels
              ? '通配符模式：本租户的成员可以使用所有已启用的对外模型。'
              : `已选择 ${allowedModels.length} 个模型。不选择任何模型表示不允许使用任何对外模型。`
          }
        />
      </fieldset>

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
