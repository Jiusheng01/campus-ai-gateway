import {useState} from 'react'
import {ChevronDown, ChevronRight, Gauge, Pencil, Plus, Users} from 'lucide-react'
import {api, type Tenant, type TenantUsage} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {TenantForm, type TenantDraft} from '../components/tenants/TenantForm'
import {TenantUsageDetail} from '../components/tenants/TenantUsageDetail'
import {limitClass, limitText} from '../components/tenants/LimitInput'
import {useResource} from '../lib/useResource'

/**
 * Tenant management, rendered as the tree it is.
 *
 * The API returns tenants in depth-first order by materialized path, so the
 * page only has to indent each row by its depth — it never sorts or rebuilds
 * the hierarchy client-side, which would let the display disagree with what
 * the enforcement path walks.
 *
 * Limits are shown in their three states. "继承" and "不限" are different
 * answers and the table must not print the same thing for both: the first
 * means an ancestor decides, the second means nobody does.
 */
export function TenantsPage({scopeTenantID}: {scopeTenantID?: string | null}) {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: Tenant[]}>('/admin/tenants')
    return d.data || []
  })
  const [draft, setDraft] = useState<TenantDraft | null>(null)
  const [usage, setUsage] = useState<{tenant: Tenant; usage: TenantUsage} | null>(null)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())

  const save = async (v: TenantDraft) => {
    try {
      // One request for both halves. PUT /admin/tenants/{id} already writes the
      // identity and the limits in a single statement, so adding a second call
      // to /allocation would write the same columns twice and turn one failure
      // into a half-applied edit. The allocation endpoint exists for clients
      // that only want to move a budget without resending a name.
      await api(v.id ? `/admin/tenants/${v.id}` : '/admin/tenants', {method: v.id ? 'PUT' : 'POST', body: JSON.stringify(v)})
      toast.success('租户已保存')
      setDraft(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存租户失败')
    }
  }

  const openUsage = async (t: Tenant) => {
    try {
      const u = await api<TenantUsage>(`/admin/tenants/${t.id}/usage`)
      setUsage({tenant: t, usage: u})
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载租户用量')
    }
  }

  const toggle = (id: string) =>
    setCollapsed(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const items = data || []

  // A subtree collapses as a whole. Hiding only the immediate children would
  // leave grandchildren floating with no visible parent, which reads as a
  // broken tree rather than a folded one.
  const hidden = new Set<string>()
  for (const t of items) {
    if (!t.path) continue
    for (const id of collapsed) {
      const parent = items.find(p => p.id === id)
      if (parent?.path && t.path.startsWith(parent.path) && t.id !== id) hidden.add(t.id)
    }
  }
  const visible = items.filter(t => !hidden.has(t.id))

  const hasChildren = (t: Tenant) => items.some(o => o.parent_id === t.id)

  return (
    <>
      <Panel
        title="租户管理"
        sub="租户是校园部门或业务系统的配额与凭证隔离边界，配额沿层级向下继承。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1, parent_id: scopeTenantID ?? null})}>
            新增租户
          </Button>
        }
      >
        {loading && !data ? (
          <SkeletonRows rows={5} columns={9} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : items.length ? (
          <>
            <div className="desktopOnly">
              <Table heads={['租户', '编码', '状态', '日 Token', '月 Token', '月请求', 'RPM', 'TPM', '并发', '操作']} minWidth={1240}>
                {visible.map(t => (
                  <tr key={t.id}>
                    <td>
                      <span className="treeCell" style={{paddingLeft: `${(t.depth || 0) * 18}px`}}>
                        {hasChildren(t) ? (
                          <button
                            className="link treeToggle"
                            onClick={() => toggle(t.id)}
                            aria-label={collapsed.has(t.id) ? '展开' : '折叠'}
                          >
                            {collapsed.has(t.id) ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
                          </button>
                        ) : (
                          <span className="treeSpacer" />
                        )}
                        <b>{t.name}</b>
                        {t.kind && t.kind !== 'tenant' && <span className="eyebrow kindTag">{t.kind}</span>}
                      </span>
                    </td>
                    <td className="mono">{t.code}</td>
                    <td>
                      <StatusBadge status={t.status} />
                    </td>
                    <td className={`numeric ${limitClass(t.daily_token_limit)}`}>{limitText(t.daily_token_limit)}</td>
                    <td className={`numeric ${limitClass(t.monthly_token_limit)}`}>{limitText(t.monthly_token_limit)}</td>
                    <td className={`numeric ${limitClass(t.monthly_request_limit)}`}>{limitText(t.monthly_request_limit)}</td>
                    {/* The tenant is the rate-limit boundary (ADR-0015); these
                        three are shared by every key it owns. */}
                    <td className={`numeric ${limitClass(t.rpm_limit)}`}>{limitText(t.rpm_limit)}</td>
                    <td className={`numeric ${limitClass(t.tpm_limit)}`}>{limitText(t.tpm_limit)}</td>
                    <td className={`numeric ${limitClass(t.max_concurrency)}`}>{limitText(t.max_concurrency)}</td>
                    <td className="rowActions">
                      <button className="link" onClick={() => void openUsage(t)}>
                        查看用量
                      </button>
                      {/* A scoped administrator cannot rewrite the tenant they
                          are scoped to — that row holds the ceiling someone
                          above them set. The API refuses it, so the button is
                          disabled here instead of producing a 403. */}
                      <button
                        className="link"
                        onClick={() => setDraft(t)}
                        disabled={t.id === scopeTenantID}
                        title={t.id === scopeTenantID ? '不能修改自己所属租户的配额，请让上级管理员调整' : undefined}
                      >
                        编辑
                      </button>
                    </td>
                  </tr>
                ))}
              </Table>
            </div>

            <div className="mobileOnly cardsList">
              {visible.map(t => (
                <article className="miniCard" key={t.id}>
                  <header>
                    <div>
                      <span className="eyebrow">{t.code}</span>
                      <h3 style={{paddingLeft: `${(t.depth || 0) * 12}px`}}>{t.name}</h3>
                    </div>
                    <StatusBadge status={t.status} />
                  </header>
                  <div className="miniCardRows">
                    <div>
                      <span>日 Token</span>
                      <b className={limitClass(t.daily_token_limit)}>{limitText(t.daily_token_limit)}</b>
                    </div>
                    <div>
                      <span>月 Token</span>
                      <b className={limitClass(t.monthly_token_limit)}>{limitText(t.monthly_token_limit)}</b>
                    </div>
                    <div>
                      <span>月请求</span>
                      <b className={limitClass(t.monthly_request_limit)}>{limitText(t.monthly_request_limit)}</b>
                    </div>
                    <div>
                      <span>RPM</span>
                      <b className={limitClass(t.rpm_limit)}>{limitText(t.rpm_limit)}</b>
                    </div>
                    <div>
                      <span>TPM</span>
                      <b className={limitClass(t.tpm_limit)}>{limitText(t.tpm_limit)}</b>
                    </div>
                    <div>
                      <span>并发</span>
                      <b className={limitClass(t.max_concurrency)}>{limitText(t.max_concurrency)}</b>
                    </div>
                  </div>
                  <footer className="cardActions">
                    <Button size="sm" icon={<Gauge />} onClick={() => void openUsage(t)}>
                      查看用量
                    </Button>
                    <Button size="sm" icon={<Pencil />} onClick={() => setDraft(t)} disabled={t.id === scopeTenantID}>
                      编辑
                    </Button>
                  </footer>
                </article>
              ))}
            </div>
          </>
        ) : (
          <EmptyState
            icon={<Users />}
            title="暂无租户"
            description="创建租户后即可为业务系统签发 API 密钥并设置配额。"
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1, parent_id: scopeTenantID ?? null})}>
                新增租户
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title={draft.id ? '编辑租户' : '新增租户'}
          subtitle="配额三态：留空继承上级，0 明确不限额，大于 0 为本级上限。限流为本租户内所有密钥共享。"
          onClose={() => setDraft(null)}
        >
          <TenantForm value={draft} tenants={items} onSave={v => void save(v)} onCancel={() => setDraft(null)} />
        </Drawer>
      )}

      {usage && (
        <Drawer title="租户用量" subtitle={usage.tenant.name} onClose={() => setUsage(null)}>
          <TenantUsageDetail tenant={usage.tenant} usage={usage.usage} />
        </Drawer>
      )}
    </>
  )
}
