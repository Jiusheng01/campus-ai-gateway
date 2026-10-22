import {useState} from 'react'
import {Layers} from 'lucide-react'
import type {ModelConfig, Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {ModelScopePicker} from '../models/ModelScopePicker'
import {LimitInput} from './LimitInput'

export type TenantDraft = Partial<Tenant> & {id?: string}

/**
 * Tenant kind is a free-text column with a few known values. It is a label for
 * people, not a permission: nothing in the enforcement path branches on it.
 */
const KINDS: {value: string; label: string}[] = [
  {value: 'school', label: '学校'},
  {value: 'dept', label: '学院 / 部门'},
  {value: 'teacher', label: '教师'},
  {value: 'student', label: '学生'},
  {value: 'tenant', label: '通用租户'},
]

export function TenantForm({
  value,
  tenants = [],
  models = [],
  onSave,
  onCancel,
}: {
  value: TenantDraft
  /** Candidates for the parent select. A tenant cannot be its own ancestor. */
  tenants?: Tenant[]
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

  const setLimit = (k: keyof TenantDraft, n: number | null) => setV(prev => ({...prev, [k]: n}))

  // A tenant cannot be parented to itself or to one of its own descendants:
  // that would make the subtree a cycle and the path prefix meaningless. The
  // server enforces it too; this only keeps the option off the list.
  const parentOptions = tenants.filter(t => t.id !== v.id && !isDescendantOf(tenants, t, v.id))

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
        <Field label="上级租户" hint="留空即根租户。配额与模型额度沿这条链向下继承。">
          <select
            value={v.parent_id || ''}
            onChange={e => setV({...v, parent_id: e.target.value || null})}
            // Reparenting an existing tenant rewrites the paths of its whole
            // subtree, so it is a separate operation this form does not offer.
            disabled={!isNew}
          >
            <option value="">（无，作为根租户）</option>
            {parentOptions.map(t => (
              <option key={t.id} value={t.id}>
                {'　'.repeat(Math.max(0, t.depth || 0))}
                {t.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="类型" hint="仅用于展示分组，不影响鉴权。">
          <select value={v.kind || 'tenant'} onChange={e => setV({...v, kind: e.target.value})}>
            {KINDS.map(k => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <p className="fieldHint">
        配额三态：<b>留空</b> 继承上级，<b>0</b> 明确不限额，<b>大于 0</b> 为本级上限。
      </p>

      <div className="formGrid">
        <Field label="每日 Token 上限">
          <LimitInput value={v.daily_token_limit} onChange={n => setLimit('daily_token_limit', n)} />
        </Field>
        <Field label="每月 Token 上限">
          <LimitInput value={v.monthly_token_limit} onChange={n => setLimit('monthly_token_limit', n)} />
        </Field>
        <Field label="每月请求上限">
          <LimitInput value={v.monthly_request_limit} onChange={n => setLimit('monthly_request_limit', n)} />
        </Field>
      </div>

      {/* Rate limits are the tenant's, not the key's: every key under this
          tenant shares these three. */}
      <div className="formGrid">
        <Field label="RPM 上限" hint="每分钟请求数，本租户所有密钥共享。">
          <LimitInput value={v.rpm_limit} onChange={n => setLimit('rpm_limit', n)} />
        </Field>
        <Field label="TPM 上限" hint="每分钟 Token 数，本租户所有密钥共享。">
          <LimitInput value={v.tpm_limit} onChange={n => setLimit('tpm_limit', n)} />
        </Field>
        <Field label="最大并发" hint="本租户同时在处理的请求数。">
          <LimitInput value={v.max_concurrency} onChange={n => setLimit('max_concurrency', n)} />
        </Field>
      </div>

      {/* The Tenant allowlist is the ceiling for personal API Keys written by
          this Tenant's members. It is not a data-plane check: existing keys keep
          their own scope, so editing this value never changes live traffic. */}
      <fieldset className="formSectionFieldset">
        <legend className="formSectionLegend">
          <Layers aria-hidden /> 模型授权
        </legend>

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

/**
 * isDescendantOf reports whether candidate sits under ancestorID.
 *
 * It reads the materialized path the API already returns rather than walking
 * parent_id pointers, so the check is one string comparison and cannot loop on
 * a cycle that a bad row introduced.
 */
function isDescendantOf(tenants: Tenant[], candidate: Tenant, ancestorID?: string): boolean {
  if (!ancestorID || !candidate.path) return false
  const ancestor = tenants.find(t => t.id === ancestorID)
  if (!ancestor?.path) return false
  return candidate.path.startsWith(ancestor.path) && candidate.path !== ancestor.path
}
