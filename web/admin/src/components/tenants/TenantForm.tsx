import {useState} from 'react'
import {Layers, Sparkles, X} from 'lucide-react'
import type {ModelConfig, Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'

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
  // added later), a named list, and [] (nothing). The chips and dropdown are
  // the existing model multi-select pattern from the API Key form, reused here
  // so an operator does not have to learn a second way to pick models.
  const allowed = v.allowed_models ?? []
  const allModels = allowed.includes('*')
  const toggleAllModels = (on: boolean) =>
    setV(prev => ({...prev, allowed_models: on ? ['*'] : []}))
  const toggleModel = (alias: string, on: boolean) =>
    setV(prev => {
      const current = prev.allowed_models ?? []
      const next = on ? [...current.filter(x => x !== '*'), alias] : current.filter(x => x !== alias)
      return {...prev, allowed_models: next}
    })

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

        <div className="multiSelectWrapper">
          <div className="multiSelectHead">
            <span className="multiSelectTitle">成员可用的对外模型</span>
            <label className="wildcardToggleLabel">
              <input
                type="checkbox"
                checked={allModels}
                onChange={e => toggleAllModels(e.target.checked)}
              />
              <span>全部对外模型（含以后新增）</span>
            </label>
          </div>

          {allModels ? (
            <div className="wildcardActiveNotice">
              <Sparkles aria-hidden />
              <div>
                <b>已启用通配符授权</b>
                <p>该租户的成员可以调用系统内所有已启用的对外模型，以后新增的模型也会自动允许。</p>
              </div>
            </div>
          ) : (
            <>
              <div className="chipsContainer">
                {allowed.length ? (
                  allowed.map(alias => {
                    const m = models.find(x => x.alias === alias)
                    return (
                      <span className="modelSelectChip" key={alias}>
                        <span className="chipAlias mono">{alias}</span>
                        {m?.display_name && <span className="chipName">({m.display_name})</span>}
                        <button
                          type="button"
                          className="chipRemoveButton"
                          onClick={() => toggleModel(alias, false)}
                          title="移除模型"
                        >
                          <X aria-hidden />
                        </button>
                      </span>
                    )
                  })
                ) : (
                  <span className="chipsEmptyPlaceholder">
                    未选择任何模型（该租户的成员将无法签发可用模型）
                  </span>
                )}
              </div>

              <div className="addModelDropdownRow">
                <select
                  className="addModelSelect"
                  value=""
                  onChange={e => {
                    const alias = e.target.value
                    if (alias) toggleModel(alias, true)
                  }}
                >
                  <option value="">+ 选择要添加的模型授权…</option>
                  {models
                    .filter(m => !allowed.includes(m.alias))
                    .map(m => (
                      <option key={m.id} value={m.alias}>
                        {m.alias} {m.display_name ? `(${m.display_name})` : ''}
                      </option>
                    ))}
                </select>
              </div>
            </>
          )}

          <small className="fieldHint">
            {allModels
              ? '通配符模式：本租户的成员可以使用所有已启用的对外模型。'
              : `已选择 ${allowed.length} 个模型。不选择任何模型表示不允许使用任何对外模型。`}
          </small>
        </div>
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
