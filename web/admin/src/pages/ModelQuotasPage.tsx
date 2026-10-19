import {useState} from 'react'
import {Gauge, Plus, Trash2} from 'lucide-react'
import {api, type ModelQuota, type Tenant} from '../api'
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
}

const emptyDraft = (): Draft => ({tenant_id: '', model_alias: '', daily_token_limit: null, monthly_token_limit: null})

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
    const [q, t] = await Promise.all([api<{data: ModelQuota[]}>('/admin/model-quotas'), api<{data: Tenant[]}>('/admin/tenants')])
    return {quotas: q.data || [], tenants: t.data || []}
  })
  const [draft, setDraft] = useState<Draft | null>(null)

  const quotas = data?.quotas || []
  const tenants = data?.tenants || []

  const save = async (d: Draft) => {
    if (!d.model_alias.trim()) {
      toast.error('请填写模型别名')
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
        sub="按「主体 + 模型」设定 Token 上限。主体为全校共享池或某个租户；删除一行即恢复继承。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setDraft(emptyDraft())}>
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
                <td className="mono">{q.model_alias}</td>
                <td className={`numeric ${limitClass(q.daily_token_limit)}`}>{limitText(q.daily_token_limit)}</td>
                <td className={`numeric ${limitClass(q.monthly_token_limit)}`}>{limitText(q.monthly_token_limit)}</td>
                <td className="rowActions">
                  <button
                    className="link"
                    onClick={() =>
                      setDraft({
                        tenant_id: q.tenant_id || '',
                        model_alias: q.model_alias,
                        daily_token_limit: q.daily_token_limit,
                        monthly_token_limit: q.monthly_token_limit,
                      })
                    }
                    // The pool row is only writable by a global administrator.
                    disabled={!q.tenant_id ? !canWritePool : false}
                  >
                    编辑
                  </button>
                  <button
                    className="link danger"
                    onClick={() => void remove(q)}
                    disabled={!q.tenant_id ? !canWritePool : false}
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
            description="没有配置时，所有租户对任意模型都不设单独上限，只受租户配额约束。"
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setDraft(emptyDraft())}>
                新增额度
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title="模型额度"
          subtitle="同一「主体 + 模型」只保留一行；再次保存即覆盖。留空表示继承，0 表示不限。"
          onClose={() => setDraft(null)}
        >
          <form
            className="form"
            onSubmit={e => {
              e.preventDefault()
              void save(draft)
            }}
          >
            <Field label="主体" hint="全校共享池是每个租户用量都会计入的那一层。">
              <select value={draft.tenant_id} onChange={e => setDraft({...draft, tenant_id: e.target.value})}>
                <option value="">全校（共享池）</option>
                {tenants.map(t => (
                  <option key={t.id} value={t.id}>
                    {'　'.repeat(Math.max(0, t.depth || 0))}
                    {t.name}
                  </option>
                ))}
              </select>
            </Field>

            <Field label="模型别名" hint="对外暴露的模型名，例如 campus-smart。">
              <input
                className="mono"
                value={draft.model_alias}
                onChange={e => setDraft({...draft, model_alias: e.target.value})}
                placeholder="campus-smart"
              />
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
