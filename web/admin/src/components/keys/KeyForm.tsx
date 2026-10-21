import {Fragment, useMemo, useState} from 'react'
import {Gauge, KeyRound, Layers, Shield} from 'lucide-react'
import type {ApiKey, ModelConfig, Tenant} from '../../api'
import {Button} from '../ui/Button'
import {Field} from '../ui/Field'
import {useConfirm} from '../ui/ConfirmDialog'
import {ModelScopePicker} from '../models/ModelScopePicker'
import {quotaState, quotaText} from '../../lib/quota'

export type KeyDraft = Partial<ApiKey> & {id?: string}

/** The three form sections, in the order the step bar presents them. */
type SectionKey = 'basic' | 'models' | 'security'

const SECTIONS: {key: SectionKey; label: string}[] = [
  {key: 'basic', label: '基础信息'},
  {key: 'models', label: '模型授权'},
  {key: 'security', label: '网络与安全边界'},
]

/**
 * One limit in whichever of the three states it is in. The state and the text
 * come from lib/quota, which also feeds the tenant table, so the two surfaces
 * cannot describe the same number differently. `className` carries the
 * surrounding metric styling so the label reads as one value.
 */
export function LimitValue({
  limit,
  unit,
  className = '',
}: {
  limit?: number | null
  unit?: string
  className?: string
}) {
  const state = quotaState(limit)
  if (state === 'unlimited') {
    return <span className={`${className} quotaUnlimitedTag`.trim()}>{quotaText(limit)}</span>
  }
  if (state === 'unset') {
    return <span className={`${className} quotaUnsetTag`.trim()}>{quotaText(limit)}</span>
  }
  return (
    <span className={`${className} quotaNumericValue mono`.trim()}>
      {quotaText(limit)}
      {unit ? ` ${unit}` : ''}
    </span>
  )
}

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
  const [v, setV] = useState<KeyDraft>(() => ({
    ...value,
    tenant_id: value.tenant_id || tenants[0]?.id || '',
    status: value.status ?? 1,
    allowed_models: value.allowed_models ?? [],
    allowed_ips: value.allowed_ips ?? [],
  }))
  // The step bar tracks the section the operator is working in. It starts at
  // the first section because that is where a new form begins, and it only
  // moves when focus really lands inside another section.
  const [activeSection, setActiveSection] = useState<SectionKey>('basic')

  const isNew = !value.id
  const allowed = v.allowed_models || []
  const allModels = allowed.includes('*')

  // Find currently selected tenant to display live quota preview
  const selectedTenant = useMemo(() => {
    return tenants.find(t => t.id === v.tenant_id) || tenants[0]
  }, [tenants, v.tenant_id])

  // The picker owns the state change; this hook only asks whether the operator
  // really meant to turn a wildcard into an explicit (and initially empty)
  // whitelist. Returning false leaves the scope untouched. The sync return for
  // the no-confirmation path matters: clicking the checkbox must update it in
  // the same event, not a microtask later.
  const changeAllModels = (on: boolean): boolean | Promise<boolean> => {
    if (on || !allModels) return true
    return confirm({
      title: '将通配符 API Key 转为显式白名单？',
      description: `转换后未勾选的 ${models.length} 个模型将立即失去访问权限；以后新增的模型也不会自动允许。`,
      confirmText: '确认转换',
      danger: true,
    })
  }

  // Quick preset helpers for expiration date
  const setExpireDays = (days: number | null) => {
    if (days === null) {
      setV(prev => ({...prev, expire_at: undefined}))
      return
    }
    const d = new Date()
    d.setDate(d.getDate() + days)
    d.setHours(23, 59, 59, 0)
    setV(prev => ({...prev, expire_at: d.toISOString()}))
  }

  // Each section names itself; adding, reordering or relabelling one only
  // touches this attribute. Focus handling sits on the fieldset rather than on
  // its controls, because focus events bubble: one handler covers text inputs,
  // selects, checkboxes and the chip buttons alike. Only focus-in changes the
  // step — leaving the form for the drawer's chrome keeps the last section,
  // which is more truthful than snapping back and blanking the bar.
  const sectionProps = (key: SectionKey) => ({
    'data-section': key,
    onFocusCapture: () => setActiveSection(key),
  })

  return (
    <form
      className="keyFormStack"
      onSubmit={e => {
        e.preventDefault()
        onSave(v)
      }}
    >
      {/* Step indicator: three sections, one active */}
      <div className="formStepsBar" role="group" aria-label="表单进度">
        {SECTIONS.map((section, index) => {
          const active = section.key === activeSection
          return (
            <Fragment key={section.key}>
              {index > 0 && <div className="stepDivider" />}
              <div
                className={`formStep ${active ? 'active' : ''}`}
                aria-current={active ? 'step' : undefined}
              >
                <span className="stepNumber">{index + 1}</span>
                <span className="stepLabel">{section.label}</span>
              </div>
            </Fragment>
          )
        })}
      </div>

      {/* Section 1: Basic Information */}
      <fieldset className="formSectionFieldset" {...sectionProps('basic')}>
        <legend className="formSectionLegend">
          <KeyRound aria-hidden /> 1. 基础信息
        </legend>

        <Field
          label="名称 *"
          hint="便于识别的用途说明，例如 教学系统 - 生产环境。"
        >
          <input
            required
            placeholder="请输入 API Key 名称"
            value={v.name || ''}
            onChange={e => setV({...v, name: e.target.value})}
          />
        </Field>

        <Field
          label="所属租户 *"
          hint="API Key 归属的租户部门，将继承该租户的限流与配额规则。"
        >
          <select
            value={v.tenant_id || tenants[0]?.id || ''}
            onChange={e => setV({...v, tenant_id: e.target.value})}
          >
            {tenants.map(t => (
              <option key={t.id} value={t.id}>
                {t.name}（{t.code}）
              </option>
            ))}
          </select>
        </Field>
      </fieldset>

      {/* Section 2: Model Authorization (Easy multi-select component) */}
      <fieldset className="formSectionFieldset" {...sectionProps('models')}>
        <legend className="formSectionLegend">
          <Layers aria-hidden /> 2. 模型授权
        </legend>

        <ModelScopePicker
          value={allowed}
          onChange={next => setV(prev => ({...prev, allowed_models: next}))}
          options={models.map(m => ({alias: m.alias, displayName: m.display_name}))}
          title="允许调用的对外模型 *"
          wildcardLabel="全部对外模型（含以后新增）"
          wildcardNoticeTitle="已启用通配符授权"
          wildcardNoticeText="当前 API Key 可以调用系统内所有已发布的对外模型，未来新发布的模型也将自动允许调用。"
          emptyPlaceholder="未选择任何模型（不勾选模型时将无法调用任何接口）"
          addPlaceholder="+ 选择要添加的模型授权…"
          hint={
            allModels
              ? '通配符模式：适用于校园统一网关管理系统。'
              : `已授权 ${allowed.length} 个模型。不选模型时不允许调用任何对外模型。`
          }
          confirmWildcardChange={changeAllModels}
        />
      </fieldset>

      {/* Section 3: Network & Security */}
      <fieldset className="formSectionFieldset" {...sectionProps('security')}>
        <legend className="formSectionLegend">
          <Shield aria-hidden /> 3. 网络与安全边界
        </legend>

        <Field
          label="允许 IP / CIDR 白名单"
          hint="每行一个，例如 192.168.1.0/24。留空表示不限制客户端来源地址。"
        >
          <textarea
            rows={3}
            placeholder="例如: 192.168.1.0/24，每行一个"
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

        <Field
          label="过期时间"
          hint="留空表示永久有效。支持 ISO 8601 格式，例如 2026-12-31T23:59:59Z。"
        >
          <div className="expireInputRow">
            <input
              className="mono expireInput"
              placeholder="永久有效 (留空)"
              value={v.expire_at || ''}
              onChange={e => setV({...v, expire_at: e.target.value || undefined})}
            />
            <div className="expirePresetButtons">
              <button
                type="button"
                className="presetBtn"
                onClick={() => setExpireDays(30)}
              >
                30天
              </button>
              <button
                type="button"
                className="presetBtn"
                onClick={() => setExpireDays(90)}
              >
                90天
              </button>
              <button
                type="button"
                className="presetBtn"
                onClick={() => setExpireDays(365)}
              >
                1年
              </button>
              <button
                type="button"
                className="presetBtn textBtn"
                onClick={() => setExpireDays(null)}
              >
                永久有效
              </button>
            </div>
          </div>
        </Field>

        <Field label="API Key 状态">
          <select
            value={v.status || 1}
            onChange={e => setV({...v, status: +e.target.value})}
          >
            <option value="1">启用（正常调用）</option>
            <option value="2">停用（拒绝调用）</option>
          </select>
        </Field>
      </fieldset>

      {/* Tenant quota preview: the API Key inherits these limits, it does not carry its own */}
      {selectedTenant && (
        <div className="tenantQuotaPreviewCard">
          <div className="quotaPreviewHead">
            <div className="quotaPreviewTitle">
              <Gauge aria-hidden />
              <b>租户配额预览</b>
            </div>
            <span className="quotaPreviewSub">
              基于所选租户「<b>{selectedTenant.name}</b>」，该 API Key 将继承并共享以下配额上限：
            </span>
          </div>

          <div className="quotaPreviewGrid">
            <div className="quotaPreviewMetric">
              <span className="quotaMetricLabel">每日 Token 上限</span>
              <LimitValue className="quotaMetricValue" limit={selectedTenant.daily_token_limit} />
            </div>
            <div className="quotaPreviewMetric">
              <span className="quotaMetricLabel">每月请求上限</span>
              <LimitValue className="quotaMetricValue" limit={selectedTenant.monthly_request_limit} />
            </div>
            <div className="quotaPreviewMetric">
              <span className="quotaMetricLabel">RPM 限制</span>
              <LimitValue
                className="quotaMetricValue"
                limit={selectedTenant.rpm_limit}
                unit="/ 分钟"
              />
            </div>
          </div>

          <div className="quotaPreviewFooter">
            <small>
              提示：限流与配额定义在租户上，本租户名下的所有 API Key 共享上述额度；单个 API Key 无法单独调高。
            </small>
          </div>
        </div>
      )}

      {/* Drawer Action Buttons */}
      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit">
          {isNew ? '创建 API Key' : '保存修改'}
        </Button>
      </div>
    </form>
  )
}
