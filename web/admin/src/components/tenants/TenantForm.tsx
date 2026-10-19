import {useState} from 'react'
import type {Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {LimitInput} from './LimitInput'

export type TenantDraft = Partial<Tenant> & {id?: string}

/**
 * Tenant kind is a free-text column with a few known values, so the select
 * offers the vocabulary the gateway knows and lets a deployment add its own
 * level by typing it. It is a label for people, not a permission: nothing in
 * the enforcement path branches on it.
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
  tenants,
  onSave,
  onCancel,
}: {
  value: TenantDraft
  /** Candidates for the parent select. A tenant cannot be its own ancestor. */
  tenants: Tenant[]
  onSave: (v: TenantDraft) => void
  onCancel: () => void
}) {
  const [v, setV] = useState<TenantDraft>(value)
  const isNew = !value.id

  const setLimit = (k: keyof TenantDraft, n: number | null) => setV({...v, [k]: n})

  // A tenant cannot be parented to itself or to one of its own descendants:
  // that would make the subtree a cycle and the path prefix meaningless. The
  // server enforces it too; this only keeps the option off the list.
  const parentOptions = tenants.filter(t => t.id !== v.id && !isDescendantOf(tenants, t, v.id))

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
        <Field label="上级租户" hint="留空即根租户。配额与模型权限沿这条链向下继承。">
          <select
            value={v.parent_id || ''}
            onChange={e => setV({...v, parent_id: e.target.value || null})}
            // Reparenting an existing tenant rewrites the paths of its whole
            // subtree, so it is a separate operation the console does not offer
            // from this form. Create the node in the right place instead.
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

      {/* Rate limits are the tenant's, not the key's (ADR-0015): every key under
          this tenant shares these three. */}
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
