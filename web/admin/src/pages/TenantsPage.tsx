import {useMemo, useState} from 'react'
import {
  ChevronDown,
  ChevronRight,
  Gauge,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Users,
} from 'lucide-react'
import {
  api,
  allocateTenant,
  deleteTenant,
  type ApiKey,
  type ModelConfig,
  type Tenant,
  type TenantAllocation,
  type TenantUsage,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {TenantForm, type TenantDraft} from '../components/tenants/TenantForm'
import {TenantUsageDetail} from '../components/tenants/TenantUsageDetail'
import {limitClass, limitText} from '../components/tenants/LimitInput'
import {useResource} from '../lib/useResource'

const LIMIT_KEYS = [
  'daily_token_limit',
  'monthly_token_limit',
  'monthly_request_limit',
  'rpm_limit',
  'tpm_limit',
  'max_concurrency',
] as const

/**
 * Tenant management, rendered as the tree it is.
 *
 * The API returns tenants in depth-first order by materialized path, so the
 * page only indents each row by its depth — it never re-sorts or rebuilds the
 * hierarchy client-side, which would let the display disagree with the chain
 * the enforcement path walks.
 *
 * Limits are shown in their three states. "继承" and "不限" are different
 * answers: the first means an ancestor decides, the second means nobody does.
 */
export function TenantsPage({scopeTenantID}: {scopeTenantID?: string | null}) {
  const toast = useToast()
  const confirm = useConfirm()
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    // The models and keys are read here because this page is the tenant
    // console: the allowlist picker needs the catalog and the delete
    // confirmation names the credentials that go with the row.
    const [tenants, models, keys] = await Promise.all([
      api<{data: Tenant[]}>('/admin/tenants'),
      api<{data: ModelConfig[]}>('/admin/models'),
      api<{data: ApiKey[]}>('/admin/api-keys'),
    ])
    return {tenants: tenants.data || [], models: models.data || [], keys: keys.data || []}
  })

  const [draft, setDraft] = useState<TenantDraft | null>(null)
  const [usage, setUsage] = useState<{tenant: Tenant; usage: TenantUsage} | null>(null)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())
  const [searchQuery, setSearchQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('')

  const tenants = data?.tenants || []
  const models = data?.models || []
  const keys = data?.keys || []

  const keyCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const k of keys) counts[k.tenant_id] = (counts[k.tenant_id] || 0) + 1
    return counts
  }, [keys])

  const save = async (v: TenantDraft) => {
    // The server rejects a tenant without both identity fields, so checking here
    // keeps the drawer open and answers in Chinese instead of surfacing the
    // API's "code and name are required" toast.
    const code = (v.code || '').trim()
    const name = (v.name || '').trim()
    if (!code || !name) {
      toast.error('请填写租户编码和租户名称')
      return
    }
    try {
      if (!v.id) {
        await api('/admin/tenants', {method: 'POST', body: JSON.stringify({...v, code, name})})
        toast.success('租户已创建')
      } else {
        // Identity and allocation are separate endpoints. Sending the limits to
        // PUT would look like it saved them and quietly change nothing.
        await api(`/admin/tenants/${v.id}`, {
          method: 'PUT',
          body: JSON.stringify({
            code,
            name,
            status: v.status,
            kind: v.kind,
            allowed_models: v.allowed_models,
          }),
        })
        const original = tenants.find(t => t.id === v.id)
        if (limitsChanged(original, v)) {
          await allocateTenant(v.id, allocationOf(v, original))
        }
        toast.success('租户已保存')
      }
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

  /**
   * Deleting a tenant removes its API Keys with it. The confirmation states how
   * many credentials go with the row; an operator who only meant to retire the
   * department should disable it instead, which leaves the keys in place.
   *
   * The member Accounts are a separate fact: their tenant_id is ON DELETE SET
   * NULL, so those people keep their accounts and become unassigned.
   */
  const remove = async (t: Tenant) => {
    const owned = keyCounts[t.id] || 0
    const members = '租户下的成员账号不会被删除，但会变为「未分配」状态（租户归属被清除），需要管理员重新分配租户。'
    const ok = await confirm({
      title: `删除租户「${t.name}」？`,
      description:
        owned > 0
          ? `该租户和它名下的 ${owned} 个 API Key 会被永久删除，这些 Key 立即失效且无法恢复。${members}历史请求日志会保留，但无法再关联回这个租户。如果只是要停止服务，请改用「停用」。`
          : `该租户会被永久删除。${members}历史请求日志会保留，但无法再关联回这个租户。如果只是要停止服务，请改用「停用」。`,
      confirmText: '确认删除',
      danger: true,
      requireText: t.name,
      requireTextLabel: `输入租户名称「${t.name}」以确认`,
    })
    if (!ok) return
    try {
      const out = await deleteTenant(t.id)
      toast.success(
        out.deleted_keys > 0 ? `已删除租户「${t.name}」及 ${out.deleted_keys} 个 API Key` : `已删除租户「${t.name}」`,
      )
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除租户失败')
    }
  }

  const toggle = (id: string) =>
    setCollapsed(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const filtered = useMemo(() => {
    return tenants.filter(t => {
      if (statusFilter && String(t.status) !== statusFilter) return false
      if (searchQuery.trim()) {
        const q = searchQuery.toLowerCase().trim()
        if (!(t.name || '').toLowerCase().includes(q) && !(t.code || '').toLowerCase().includes(q)) return false
      }
      return true
    })
  }, [tenants, searchQuery, statusFilter])

  // A subtree collapses as a whole. Hiding only the immediate children would
  // leave grandchildren floating with no visible parent.
  const hidden = new Set<string>()
  for (const t of filtered) {
    if (!t.path) continue
    for (const id of collapsed) {
      const parent = tenants.find(p => p.id === id)
      if (parent?.path && t.path.startsWith(parent.path) && t.id !== id) hidden.add(t.id)
    }
  }
  const visible = filtered.filter(t => !hidden.has(t.id))
  const hasChildren = (t: Tenant) => tenants.some(o => o.parent_id === t.id)

  // A scoped administrator cannot rewrite the tenant it is confined to: that
  // row holds the ceiling someone above them set. The API refuses it, so the
  // buttons are disabled instead of producing a 403.
  const isOwnNode = (t: Tenant) => !!scopeTenantID && t.id === scopeTenantID
  const ownNodeHint = '不能修改自己所属租户的配额，请让上级管理员调整'

  return (
    <div className="stack">
      <Panel
        title="租户层级"
        sub={
          scopeTenantID
            ? '你管理的是所选租户及其下级。配额三态：留空继承上级，0 明确不限额，大于 0 为本级上限。'
            : '租户是校园部门或业务系统的配额与凭证隔离边界，配额沿层级向下继承。三态：留空继承上级，0 明确不限额，大于 0 为本级上限。'
        }
        actions={
          <Button
            variant="primary"
            icon={<Plus />}
            onClick={() => setDraft({status: 1, kind: 'tenant', parent_id: scopeTenantID ?? null})}
          >
            新增租户
          </Button>
        }
      >
        <div className="filterCard">
          <div className="filtersBar">
            <div className="searchFieldGroup">
              <Search className="searchFieldIcon" aria-hidden />
              <input
                className="filterSearchInput"
                placeholder="搜索租户名称或编码..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
              />
            </div>
            <label className="filterFieldInline">
              <span className="filterLabel">状态</span>
              <select className="filterSelect" value={statusFilter} onChange={e => setStatusFilter(e.target.value)}>
                <option value="">全部状态</option>
                <option value="1">启用</option>
                <option value="2">停用</option>
              </select>
            </label>
            <div className="filterActionButtons">
              <Button
                size="sm"
                icon={<RefreshCw />}
                onClick={() => {
                  setSearchQuery('')
                  setStatusFilter('')
                  reload()
                }}
              >
                重置
              </Button>
            </div>
          </div>
        </div>

        {loading && !data ? (
          <SkeletonRows rows={5} columns={10} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : visible.length ? (
          <>
          <div className="desktopOnly">
            <Table
              heads={['租户', '编码', '状态', '日 Token', '月 Token', '月请求', 'RPM', 'TPM', '并发', '操作']}
              minWidth={1240}
            >
              {visible.map(t => (
                <tr key={t.id}>
                  <td>
                    <span className="treeCell" style={{paddingLeft: `${(t.depth || 0) * 18}px`}}>
                      {hasChildren(t) ? (
                        <button
                          type="button"
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
                  <td className="mono muted">{t.code}</td>
                  <td>
                    <span className={`statusChip ${t.status === 1 ? 'ok' : 'down'}`}>
                      <i className={`dot ${t.status === 1 ? 'on' : 'down'}`} aria-hidden />
                      {t.status === 1 ? '启用' : '停用'}
                    </span>
                  </td>
                  <td className={`numeric ${limitClass(t.daily_token_limit)}`}>{limitText(t.daily_token_limit)}</td>
                  <td className={`numeric ${limitClass(t.monthly_token_limit)}`}>{limitText(t.monthly_token_limit)}</td>
                  <td className={`numeric ${limitClass(t.monthly_request_limit)}`}>
                    {limitText(t.monthly_request_limit)}
                  </td>
                  <td className={`numeric ${limitClass(t.rpm_limit)}`}>{limitText(t.rpm_limit)}</td>
                  <td className={`numeric ${limitClass(t.tpm_limit)}`}>{limitText(t.tpm_limit)}</td>
                  <td className={`numeric ${limitClass(t.max_concurrency)}`}>{limitText(t.max_concurrency)}</td>
                  <td>
                    <div className="rowActions">
                      <button type="button" className="tableActionLink" onClick={() => void openUsage(t)}>
                        <Gauge aria-hidden />
                        用量
                      </button>
                      <button
                        type="button"
                        className="tableActionLink"
                        onClick={() => setDraft(t)}
                        disabled={isOwnNode(t)}
                        title={isOwnNode(t) ? ownNodeHint : undefined}
                      >
                        <Pencil aria-hidden />
                        编辑
                      </button>
                      <button
                        type="button"
                        className="tableActionLink dangerText"
                        onClick={() => void remove(t)}
                        disabled={isOwnNode(t)}
                        title={isOwnNode(t) ? ownNodeHint : undefined}
                      >
                        <Trash2 aria-hidden />
                        删除
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </Table>
            <footer className="tableFooterBar">
              <span className="tableRecordCount">共 {visible.length} 个租户</span>
            </footer>
          </div>
          <section className="mobileOnly cardsList" aria-label="租户卡片列表">
            {visible.map(t => (
              <article className="miniCard" key={t.id}>
                <header>
                  <div>
                    <span className="eyebrow">{t.code}</span>
                    <h3 style={{paddingLeft: `${(t.depth || 0) * 12}px`}}>{t.name}</h3>
                  </div>
                  <span className={`statusChip ${t.status === 1 ? 'ok' : 'down'}`}>
                    <i className={`dot ${t.status === 1 ? 'on' : 'down'}`} aria-hidden />
                    {t.status === 1 ? '启用' : '停用'}
                  </span>
                </header>
                <div className="miniCardRows">
                  <div>
                    <span>日 Token</span>
                    <b className="tenantQuotaValue"><span className={limitClass(t.daily_token_limit)}>{limitText(t.daily_token_limit)}</span></b>
                  </div>
                  <div>
                    <span>月 Token</span>
                    <b className="tenantQuotaValue"><span className={limitClass(t.monthly_token_limit)}>{limitText(t.monthly_token_limit)}</span></b>
                  </div>
                  <div>
                    <span>月请求</span>
                    <b className="tenantQuotaValue"><span className={limitClass(t.monthly_request_limit)}>{limitText(t.monthly_request_limit)}</span></b>
                  </div>
                  <div>
                    <span>RPM</span>
                    <b className="tenantQuotaValue"><span className={limitClass(t.rpm_limit)}>{limitText(t.rpm_limit)}</span></b>
                  </div>
                  <div>
                    <span>TPM</span>
                    <b className="tenantQuotaValue"><span className={limitClass(t.tpm_limit)}>{limitText(t.tpm_limit)}</span></b>
                  </div>
                  <div>
                    <span>并发</span>
                    <b className="tenantQuotaValue"><span className={limitClass(t.max_concurrency)}>{limitText(t.max_concurrency)}</span></b>
                  </div>
                </div>
                <footer className="cardActions">
                  <Button size="sm" icon={<Gauge />} onClick={() => void openUsage(t)}>
                    用量
                  </Button>
                  <Button size="sm" icon={<Pencil />} onClick={() => setDraft(t)} disabled={isOwnNode(t)}>
                    编辑
                  </Button>
                  <Button
                    size="sm"
                    variant="danger"
                    icon={<Trash2 />}
                    onClick={() => void remove(t)}
                    disabled={isOwnNode(t)}
                  >
                    删除
                  </Button>
                </footer>
              </article>
            ))}
          </section>
          </>
        ) : (
          <EmptyState
            icon={<Users />}
            title="暂无租户"
            description="创建租户后即可为业务系统签发 API 密钥并设置配额。"
            action={
              <Button
                variant="primary"
                icon={<Plus />}
                onClick={() => setDraft({status: 1, kind: 'tenant', parent_id: scopeTenantID ?? null})}
              >
                新增租户
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          size="lg"
          title={draft.id ? '编辑租户' : '新增租户'}
          subtitle="配额三态：留空继承上级，0 明确不限额，大于 0 为本级上限。限流为本租户内所有密钥共享。"
          onClose={() => setDraft(null)}
        >
          <TenantForm
            value={draft}
            tenants={tenants}
            models={models}
            onSave={v => void save(v)}
            onCancel={() => setDraft(null)}
          />
        </Drawer>
      )}

      {usage && (
        <Drawer size="lg" title="租户用量" subtitle={usage.tenant.name} onClose={() => setUsage(null)}>
          <TenantUsageDetail tenant={usage.tenant} usage={usage.usage} />
        </Drawer>
      )}
    </div>
  )
}

/** limitsChanged compares only the six allocation fields, not identity. */
function limitsChanged(original: Tenant | undefined, draft: TenantDraft): boolean {
  if (!original) return true
  return LIMIT_KEYS.some(k => (original[k] ?? null) !== (draft[k] ?? null))
}

/**
 * allocationOf builds the full replacement payload. An omitted key means
 * inherit, so every limit is sent even when only one moved; the stored budget
 * is carried across because the console does not edit it.
 */
function allocationOf(draft: TenantDraft, original?: Tenant): TenantAllocation {
  return {
    daily_token_limit: draft.daily_token_limit ?? null,
    monthly_token_limit: draft.monthly_token_limit ?? null,
    monthly_request_limit: draft.monthly_request_limit ?? null,
    monthly_budget_micros: original?.monthly_budget_micros ?? draft.monthly_budget_micros ?? 0,
    rpm_limit: draft.rpm_limit ?? null,
    tpm_limit: draft.tpm_limit ?? null,
    max_concurrency: draft.max_concurrency ?? null,
  }
}
