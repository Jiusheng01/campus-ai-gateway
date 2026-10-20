import {useState} from 'react'
import {Gauge, Plus, Trash2} from 'lucide-react'
import {api, type ModelConfig, type ModelQuota, type Tenant} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {Field} from '../components/ui/Field'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {LimitInput, limitClass, limitText} from '../components/tenants/LimitInput'
import {useResource} from '../lib/useResource'

type Draft = {
  /** Empty string is the campus-wide pool; the API receives null for it. */
  tenant_id: string
  model_alias: string
  daily_token_limit: number | null
  monthly_token_limit: number | null
  /**
   * True when the drawer was opened from an existing row.
   *
   * The write is an upsert keyed on (subject, model), so editing a row and
   * changing either half does not move it — it creates a second row and leaves
   * the first one in place, which reads as "my edit did nothing". The subject
   * and the alias are therefore fixed while editing; changing them means
   * deleting the row and adding the right one.
   */
  editing?: boolean
}

const emptyDraft = (tenantID = ''): Draft => ({
  tenant_id: tenantID,
  model_alias: '',
  daily_token_limit: null,
  monthly_token_limit: null,
})

/**
 * Per-model quotas.
 *
 * Two axes meet here: the subject (one tenant, or the campus-wide pool that
 * every tenant's usage also lands on) and the model. A row caps one cell of
 * that grid; deleting it means "inherit", which is why DELETE is the way back
 * to the default rather than setting a sentinel value.
 *
 * The pool row is listed first and cannot be written by a scoped
 * administrator, so the page marks it instead of offering a button the API
 * will refuse.
 */
export function ModelQuotasPage({scopeTenantID}: {scopeTenantID?: string | null}) {
  const toast = useToast()
  const confirm = useConfirm()
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const [q, t, m] = await Promise.all([
      api<{data: ModelQuota[]}>('/admin/model-quotas'),
      api<{data: Tenant[]}>('/admin/tenants'),
      api<{data: ModelConfig[]}>('/admin/models'),
    ])
    return {quotas: q.data || [], tenants: t.data || [], models: m.data || []}
  })
  const [draft, setDraft] = useState<Draft | null>(null)

  const quotas = data?.quotas || []
  const tenants = data?.tenants || []
  // The picker shows the catalog, so a quota can never be typed against an
  // alias that does not exist. Rows created before this rule may still name a
  // deleted model; those are listed but cannot be re-saved.
  const models = (data?.models || []).slice().sort((a, b) => (a.display_name || a.alias).localeCompare(b.display_name || b.alias))
  const modelAliases = new Set(models.map(m => m.alias))
  const modelLabel = (alias: string) => {
    const m = models.find(x => x.alias === alias)
    return m && m.display_name ? `${m.display_name}（${alias}）` : alias
  }

  // A scoped administrator may only write strict descendants. The campus pool
  // is global, and their own tenant's rows are the ceilings their parent set,
  // so neither is offered here rather than producing a 403.
  const writableTenants = scopeTenantID ? tenants.filter(t => t.id !== scopeTenantID) : tenants
  const writable = (q: ModelQuota) => (q.tenant_id ? q.tenant_id !== scopeTenantID : !scopeTenantID)
  const defaultSubject = scopeTenantID ? writableTenants[0]?.id ?? '' : ''
  const canCreate = (!scopeTenantID || writableTenants.length > 0) && models.length > 0
  const noDescendantHint = '你只能为下级租户配置模型额度；当前没有下级租户'
  const noModelHint = '请先在「模型与线路」中创建对外模型'
  const createTitle = canCreate ? undefined : models.length === 0 ? noModelHint : noDescendantHint
  const newDraft = (tenantID = ''): Draft => ({...emptyDraft(tenantID), model_alias: models[0]?.alias || ''})

  const save = async (d: Draft) => {
    if (!d.model_alias.trim()) {
      toast.error('请选择模型')
      return
    }
    // The server enforces this too; checking here saves a round trip and gives
    // the operator the reason instead of a generic 400.
    if (!modelAliases.has(d.model_alias.trim())) {
      toast.error('该模型已不存在，请重新选择')
      return
    }
    // A row whose two limits are both "inherit" caps nothing. Saving it would
    // leave a line in the table that looks like a configured quota and behaves
    // like no row at all; deleting the row is the honest way to say that.
    if (d.daily_token_limit === null && d.monthly_token_limit === null) {
      toast.error('每日与每月上限都留空等于没有配置；如要恢复继承，请删除该行')
      return
    }
    try {
      await api('/admin/model-quotas', {
        method: 'PUT',
        body: JSON.stringify({
          tenant_id: d.tenant_id || null,
          model_alias: d.model_alias.trim(),
          daily_token_limit: d.daily_token_limit,
          monthly_token_limit: d.monthly_token_limit,
        }),
      })
      toast.success('模型额度已保存')
      setDraft(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存模型额度失败')
    }
  }

  const remove = async (q: ModelQuota) => {
    const ok = await confirm({
      title: '删除模型额度',
      description: `删除后 ${q.tenant_name || '全校'} 对 ${q.model_alias} 将回到继承上级的状态。`,
      confirmText: '删除',
      danger: true,
    })
    if (!ok) return
    try {
      const params = new URLSearchParams({model_alias: q.model_alias})
      if (q.tenant_id) params.set('tenant_id', q.tenant_id)
      await api(`/admin/model-quotas?${params.toString()}`, {method: 'DELETE'})
      toast.success('已恢复为继承')
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const canWritePool = !scopeTenantID
  const subject = (q: ModelQuota) => (q.tenant_id ? q.tenant_name || q.tenant_code || q.tenant_id : '全校（共享池）')

  return (
    <>
      <Panel
        title="模型额度"
        sub={
          (scopeTenantID
            ? '按「主体 + 模型」设定 Token 上限。你只能为下级租户配置；删除一行即恢复继承。'
            : '按「主体 + 模型」设定 Token 上限。主体为全校共享池或某个租户；删除一行即恢复继承。') +
          '「生效」按链上最紧的正数上限计算，与限流器一致。'
        }
        actions={
          <Button
            variant="primary"
            icon={<Plus />}
            onClick={() => setDraft(newDraft(defaultSubject))}
            disabled={!canCreate}
            title={createTitle}
          >
            新增额度
          </Button>
        }
      >
        {loading && !data ? (
          <SkeletonRows rows={5} columns={5} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : quotas.length ? (
          <Table heads={['主体', '模型', '每日 Token', '每月 Token', '操作']} minWidth={760}>
            {quotas.map(q => (
              <tr key={`${q.tenant_id || 'pool'}-${q.model_alias}`}>
                <td>
                  <b>{subject(q)}</b>
                  {!q.tenant_id && <span className="eyebrow kindTag">全校</span>}
                </td>
                <td>
                  <span className="mono">{q.model_alias}</span>
                  {!modelAliases.has(q.model_alias) && (
                    <span className="eyebrow kindTag" title="该模型已从目录中删除，额度不会再生效">
                      模型不存在
                    </span>
                  )}
                </td>
                <td className="numeric">
                  <LimitCell
                    configured={q.daily_token_limit}
                    effective={q.effective?.daily_token_limit}
                    setBy={q.effective?.set_by}
                  />
                </td>
                <td className="numeric">
                  <LimitCell
                    configured={q.monthly_token_limit}
                    effective={q.effective?.monthly_token_limit}
                    setBy={q.effective?.set_by}
                  />
                </td>
                <td className="rowActions" title={writable(q) ? undefined : noDescendantHint}>
                  <button
                    className="link"
                    onClick={() =>
                      setDraft({
                        tenant_id: q.tenant_id || '',
                        model_alias: q.model_alias,
                        daily_token_limit: q.daily_token_limit,
                        monthly_token_limit: q.monthly_token_limit,
                        editing: true,
                      })
                    }
                    // The pool row and the scoped administrator's own tenant
                    // are read-only here: their values are the ceilings set
                    // above them, not something they may raise. An orphan
                    // alias is read-only too — the server refuses a write that
                    // names a model the catalog no longer contains.
                    disabled={!writable(q) || !modelAliases.has(q.model_alias)}
                    title={!modelAliases.has(q.model_alias) ? '该模型已不存在，只能删除这条额度' : undefined}
                  >
                    编辑
                  </button>
                  <button
                    className="link danger"
                    onClick={() => void remove(q)}
                    disabled={!writable(q)}
                    aria-label="删除"
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState
            icon={<Gauge />}
            title="暂无模型额度"
            description={
              scopeTenantID
                ? '你的下级租户还没有单独的模型上限；不配置时它们只受租户配额与上级模型额度约束。'
                : '没有配置时，所有租户对任意模型都不设单独上限，只受租户配额约束。'
            }
            action={
              <Button
                variant="primary"
                icon={<Plus />}
                onClick={() => setDraft(newDraft(defaultSubject))}
                disabled={!canCreate}
                title={createTitle}
              >
                新增额度
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title="模型额度"
          subtitle="同一「主体 + 模型」只保留一行；再次保存即覆盖。留空表示本级不设上限（上级上限仍生效），0 表示本级不限。"
          onClose={() => setDraft(null)}
        >
          <form
            className="form"
            onSubmit={e => {
              e.preventDefault()
              void save(draft)
            }}
          >
            <Field
              label="主体"
              hint={
                draft.editing
                  ? '主体不可修改：写入按「主体 + 模型」定位，改了主体只会新增一行。要换主体请删除后重建。'
                  : scopeTenantID
                    ? '你只能为下级租户配置；全校共享池与你自己的租户由上级设置。'
                    : '全校共享池是每个租户用量都会计入的那一层。'
              }
            >
              <select
                value={draft.tenant_id}
                onChange={e => setDraft({...draft, tenant_id: e.target.value})}
                disabled={draft.editing}
              >
                {/* The pool is only writable by a global administrator, so a
                    scoped one is not offered it rather than shown an option
                    that can only produce a 403. */}
                {canWritePool && <option value="">全校（共享池）</option>}
                {writableTenants.map(t => (
                  <option key={t.id} value={t.id}>
                    {'　'.repeat(Math.max(0, t.depth || 0))}
                    {t.name}
                  </option>
                ))}
              </select>
            </Field>

            <Field
              label="模型"
              hint={
                draft.editing
                  ? '模型不可修改：同一行改模型等于新增一行。要更正请删除后重建。'
                  : '从模型目录中选择；额度按这个对外模型名生效。'
              }
            >
              {draft.editing ? (
                <input className="mono" value={modelLabel(draft.model_alias)} disabled />
              ) : (
                <select
                  value={draft.model_alias}
                  onChange={e => setDraft({...draft, model_alias: e.target.value})}
                  disabled={!models.length}
                >
                  {!models.length && <option value="">暂无可选模型</option>}
                  {models.map(m => (
                    <option key={m.id} value={m.alias}>
                      {m.display_name ? `${m.display_name}（${m.alias}）` : m.alias}
                    </option>
                  ))}
                </select>
              )}
            </Field>

            <div className="formGrid">
              <Field label="每日 Token 上限">
                <LimitInput value={draft.daily_token_limit} onChange={n => setDraft({...draft, daily_token_limit: n})} />
              </Field>
              <Field label="每月 Token 上限">
                <LimitInput value={draft.monthly_token_limit} onChange={n => setDraft({...draft, monthly_token_limit: n})} />
              </Field>
            </div>

            <div className="formActions">
              <Button onClick={() => setDraft(null)}>取消</Button>
              <Button variant="primary" type="submit">
                保存
              </Button>
            </div>
          </form>
        </Drawer>
      )}
    </>
  )
}

/**
 * One limit cell: what this subject declares, and what the limiter will apply.
 *
 * The second line is omitted when it would only repeat the first (an own cap
 * that no ancestor tightens). When a value is capped above this subject, the
 * source is named -- that is the answer to "I set 300, why does it stop at 100?"
 */
function LimitCell({
  configured,
  effective,
  setBy,
}: {
  configured: number | null
  effective?: number
  setBy?: string
}) {
  const redundant = configured !== null && configured > 0 && effective === configured
  const uncapped = configured === 0 && effective === 0
  let note = ''
  if (effective !== undefined && !redundant && !uncapped && setBy) {
    if (configured === null) {
      note = `继承自 ${setBy}`
    } else if (effective > 0 && (configured === 0 || effective < configured)) {
      note = `受 ${setBy} 限制`
    }
  }
  return (
    <div className="quotaEffectiveCell">
      <span className={limitClass(configured)}>{limitText(configured)}</span>
      {!redundant && !uncapped && effective !== undefined && (
        <small className="quotaEffective">
          生效 {limitText(effective)}
          {note && <span className="quotaSource">{note}</span>}
        </small>
      )}
    </div>
  )
}
