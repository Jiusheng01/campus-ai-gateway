import {useState} from 'react'
import {Gauge, Plus, Trash2} from 'lucide-react'
import {
  api,
  deleteModelQuota,
  listModelQuotas,
  upsertModelQuota,
  type ModelConfig,
  type ModelQuotaView,
  type Tenant,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {Field} from '../components/ui/Field'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {LimitInput} from '../components/tenants/LimitInput'
import {LimitCell} from '../components/tenants/LimitCell'
import {useResource} from '../lib/useResource'

type Draft = {
  /** Empty string is the campus-wide pool; the API receives null for it. */
  tenant_id: string
  model_alias: string
  daily_token_limit: number | null
  monthly_token_limit: number | null
  /** An upsert is keyed on (subject, model), so neither half moves on an edit. */
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
 */
export function ModelQuotasPage({scopeTenantID}: {scopeTenantID?: string | null}) {
  const toast = useToast()
  const confirm = useConfirm()
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const [q, t, m] = await Promise.all([
      listModelQuotas(),
      api<{data: Tenant[]}>('/admin/tenants'),
      api<{data: ModelConfig[]}>('/admin/models'),
    ])
    return {quotas: q, tenants: t.data || [], models: m.data || []}
  })
  const [draft, setDraft] = useState<Draft | null>(null)

  const quotas = data?.quotas || []
  const tenants = data?.tenants || []
  // The picker shows the catalog, so a quota can never be typed against an
  // alias that does not exist. Rows naming a deleted model are listed but not
  // re-savable.
  const models = (data?.models || []).slice().sort((a, b) => (a.display_name || a.alias).localeCompare(b.display_name || b.alias))
  const modelAliases = new Set(models.map(m => m.alias))
  const modelLabel = (alias: string) => {
    const m = models.find(x => x.alias === alias)
    return m && m.display_name ? `${m.display_name}（${alias}）` : alias
  }

  // A scoped administrator may only write strict descendants. The campus pool
  // is global, and their own tenant's rows are the ceilings set above them.
  const writableTenants = scopeTenantID ? tenants.filter(t => t.id !== scopeTenantID) : tenants
  const writable = (q: ModelQuotaView) => (q.tenant_id ? q.tenant_id !== scopeTenantID : !scopeTenantID)
  const defaultSubject = scopeTenantID ? writableTenants[0]?.id ?? '' : ''
  const canCreate = (!scopeTenantID || writableTenants.length > 0) && models.length > 0
  const noDescendantHint = '你只能为下级租户配置模型额度；当前没有下级租户'
  const noModelHint = '请先在「对外模型」中创建模型'
  const createTitle = canCreate ? undefined : models.length === 0 ? noModelHint : noDescendantHint
  const newDraft = (tenantID = ''): Draft => ({...emptyDraft(tenantID), model_alias: models[0]?.alias || ''})

  const save = async (d: Draft) => {
    if (!d.model_alias.trim()) {
      toast.error('请选择模型')
      return
    }
    if (!modelAliases.has(d.model_alias.trim())) {
      toast.error('该模型已不存在，请重新选择')
      return
    }
    // A row whose two limits are both "inherit" caps nothing; saving it would
    // look configured and behave like no row at all.
    if (d.daily_token_limit === null && d.monthly_token_limit === null) {
      toast.error('每日与每月上限都留空等于没有配置；如要恢复继承，请删除该行')
      return
    }
    try {
      await upsertModelQuota({
        tenant_id: d.tenant_id || null,
        model_alias: d.model_alias.trim(),
        daily_token_limit: d.daily_token_limit,
        monthly_token_limit: d.monthly_token_limit,
      })
      toast.success('模型额度已保存')
      setDraft(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存模型额度失败')
    }
  }

  const remove = async (q: ModelQuotaView) => {
    const ok = await confirm({
      title: '删除模型额度',
      description: `删除后 ${q.tenant_name || '全校'} 对 ${q.model_alias} 将回到继承上级的状态。`,
      confirmText: '删除',
      danger: true,
    })
    if (!ok) return
    try {
      await deleteModelQuota(q.tenant_id, q.model_alias)
      toast.success('已恢复为继承')
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const canWritePool = !scopeTenantID
  const subject = (q: ModelQuotaView) => (q.tenant_id ? q.tenant_name || q.tenant_code || q.tenant_id : '全校（共享池）')

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
                    type="button"
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
                    disabled={!writable(q) || !modelAliases.has(q.model_alias)}
                    title={!modelAliases.has(q.model_alias) ? '该模型已不存在，只能删除这条额度' : undefined}
                  >
                    编辑
                  </button>
                  <button
                    type="button"
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
