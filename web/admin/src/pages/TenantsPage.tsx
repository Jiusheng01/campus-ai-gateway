import {useMemo, useState} from 'react'
import {
  Gauge,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Users,
} from 'lucide-react'
import {api, deleteTenant, type ModelConfig, type Tenant, type TenantUsage} from '../api'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {TenantForm, type TenantDraft} from '../components/tenants/TenantForm'
import {TenantUsageDetail} from '../components/tenants/TenantUsageDetail'
import {quotaState, quotaText} from '../lib/quota'

/**
 * The tenant table for one tenant list.
 *
 * The list is a prop rather than a local fetch: the tenant tab lives beside the
 * key table inside CallsPage, which already reads `/admin/tenants` for its
 * counters. A second loader here asked the gateway for the same list twice per
 * visit. `onReload` still reaches the page's loader, so a save or a retry
 * refreshes both views from the one request.
 */
export function TenantsPage({
  tenants,
  models = [],
  keyCounts = {},
  loading = false,
  error = '',
  errorStatus = 0,
  onReload,
}: {
  tenants: Tenant[] | null
  /** The enabled model catalog the allowlist form offers as named choices. */
  models?: ModelConfig[]
  /** API Keys per tenant id, so the delete confirmation can name the cost. */
  keyCounts?: Record<string, number>
  loading?: boolean
  error?: string
  errorStatus?: number
  onReload: () => void
}) {
  const toast = useToast()
  const confirm = useConfirm()
  const reload = onReload

  const [draft, setDraft] = useState<TenantDraft | null>(null)
  const [usage, setUsage] = useState<{tenant: Tenant; usage: TenantUsage} | null>(null)

  // Filters
  const [searchQuery, setSearchQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('')

  const save = async (v: TenantDraft) => {
    try {
      await api(v.id ? `/admin/tenants/${v.id}` : '/admin/tenants', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
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

  /**
   * Deleting a tenant removes its API Keys with it — the gateway runs both
   * deletes in one transaction, because the keys' tenant_id is a required
   * foreign key. The confirmation therefore states how many credentials go
   * with the tenant; an operator who only meant to retire the department
   * should disable it instead, which leaves the keys in place.
   */
  const remove = async (t: Tenant) => {
    const keys = keyCounts[t.id] || 0
    const ok = await confirm({
      title: `删除租户「${t.name}」？`,
      description:
        keys > 0
          ? `该租户和它名下的 ${keys} 个 API Key 会被永久删除，这些 Key 立即失效且无法恢复。历史请求日志会保留，但无法再关联回这个租户。如果只是要停止服务，请改用「停用」。`
          : '该租户会被永久删除。历史请求日志会保留，但无法再关联回这个租户。如果只是要停止服务，请改用「停用」。',
      confirmText: '确认删除',
      danger: true,
      requireText: t.name,
      requireTextLabel: `输入租户名称「${t.name}」以确认`,
    })
    if (!ok) return
    try {
      const out = await deleteTenant(t.id)
      toast.success(
        out.deleted_keys > 0
          ? `已删除租户「${t.name}」及 ${out.deleted_keys} 个 API Key`
          : `已删除租户「${t.name}」`,
      )
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除租户失败')
    }
  }

  // The table cell and the card both render through this, so the same response
  // cannot read as 无限制 in one place and 未设置 in the other. The three-way
  // classification itself lives in lib/quota so the API Key drawer's quota
  // preview can converge on the same rule.
  const renderLimit = (val?: number | null, unit = '') => {
    const state = quotaState(val)
    if (state === 'unset') {
      return <span className="quotaUnsetTag">{quotaText(val)}</span>
    }
    if (state === 'unlimited') {
      return <span className="quotaUnlimitedTag">{quotaText(val)}</span>
    }
    return (
      <span className="quotaNumericValue mono">
        {quotaText(val)} {unit && <small className="quotaUnit">{unit}</small>}
      </span>
    )
  }

  const items = tenants || []

  const filteredTenants = useMemo(() => {
    return items.filter(t => {
      if (statusFilter && String(t.status) !== statusFilter) return false
      if (searchQuery.trim()) {
        const q = searchQuery.toLowerCase().trim()
        const matchName = (t.name || '').toLowerCase().includes(q)
        const matchCode = (t.code || '').toLowerCase().includes(q)
        if (!matchName && !matchCode) return false
      }
      return true
    })
  }, [items, searchQuery, statusFilter])

  return (
    <div className="stack">
      <section className="tenantsSectionCard" aria-label="租户配额列表">
        <header className="keysSectionHeader">
          <div className="keysSectionTitleBlock">
            <h2 className="keysSectionTitle">租户配额</h2>
            <p className="keysSectionSub">
              租户是校园部门或业务系统的配额与凭证隔离边界。限流与配额为该租户名下所有 API Key 共享。
            </p>
          </div>
          <Button
            variant="primary"
            icon={<Plus />}
            onClick={() => setDraft({status: 1})}
          >
            + 新增租户
          </Button>
        </header>

        {/* Filters */}
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
              <select
                className="filterSelect"
                value={statusFilter}
                onChange={e => setStatusFilter(e.target.value)}
              >
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

        {loading && !tenants ? (
          <SkeletonRows rows={5} columns={9} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : filteredTenants.length ? (
          <>
            {/* Ten columns of limits cannot be squeezed into a phone viewport,
                so both representations are rendered and the 900px breakpoint
                in styles.css picks one. Rendering both rather than branching on
                a measured width keeps the choice in CSS, where it also holds on
                first paint. */}
            <div className="desktopOnly">
              <Table
                heads={[
                  '租户',
                  '编码',
                  '状态',
                  '日 Token 上限',
                  '月 Token 上限',
                  '月请求上限',
                  'RPM 上限',
                  'TPM 上限',
                  '并发限制',
                  '操作',
                ]}
                minWidth={1180}
              >
                {filteredTenants.map(t => (
                  <tr key={t.id}>
                    <td>
                      <div className="tenantCell">
                        <span className="tenantName">{t.name}</span>
                      </div>
                    </td>
                    <td className="mono muted">{t.code}</td>
                    <td>
                      <span className={`statusChip ${t.status === 1 ? 'ok' : 'down'}`}>
                        <i className={`dot ${t.status === 1 ? 'on' : 'down'}`} aria-hidden />
                        {t.status === 1 ? '启用' : '停用'}
                      </span>
                    </td>
                    <td className="numeric">{renderLimit(t.daily_token_limit)}</td>
                    <td className="numeric">{renderLimit(t.monthly_token_limit)}</td>
                    <td className="numeric">{renderLimit(t.monthly_request_limit)}</td>
                    <td className="numeric">{renderLimit(t.rpm_limit, '/min')}</td>
                    <td className="numeric">{renderLimit(t.tpm_limit, '/min')}</td>
                    <td className="numeric">{renderLimit(t.max_concurrency)}</td>
                    <td>
                      <div className="rowActions">
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => void openUsage(t)}
                        >
                          <Gauge aria-hidden />
                          查看用量
                        </button>
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => setDraft(t)}
                        >
                          <Pencil aria-hidden />
                          编辑
                        </button>
                        <button
                          type="button"
                          className="tableActionLink dangerText"
                          onClick={() => void remove(t)}
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
                <span className="tableRecordCount">共 {filteredTenants.length} 个租户</span>
              </footer>
            </div>

            <section className="mobileOnly cardsList" aria-label="租户卡片列表">
              {filteredTenants.map(t => (
                <article className="miniCard" key={t.id}>
                  <header>
                    <div>
                      <span className="eyebrow">{t.code}</span>
                      <h3>{t.name}</h3>
                    </div>
                    <span className={`statusChip ${t.status === 1 ? 'ok' : 'down'}`}>
                      <i className={`dot ${t.status === 1 ? 'on' : 'down'}`} aria-hidden />
                      {t.status === 1 ? '启用' : '停用'}
                    </span>
                  </header>
                  {/* The table and the card share renderLimit so the same
                      response cannot read as 无限制 in one place and 未设置 in
                      the other; the labels stay short because the grid is
                      three columns wide on a phone. The value cell carries its
                      own class because `.miniCardRows span` styles the label and
                      would otherwise flatten the quota tags nested in it. */}
                  <div className="miniCardRows">
                    <div>
                      <span>日 Token</span>
                      <b className="tenantQuotaValue">{renderLimit(t.daily_token_limit)}</b>
                    </div>
                    <div>
                      <span>月 Token</span>
                      <b className="tenantQuotaValue">{renderLimit(t.monthly_token_limit)}</b>
                    </div>
                    <div>
                      <span>月请求</span>
                      <b className="tenantQuotaValue">{renderLimit(t.monthly_request_limit)}</b>
                    </div>
                    <div>
                      <span>RPM</span>
                      <b className="tenantQuotaValue">{renderLimit(t.rpm_limit, '/min')}</b>
                    </div>
                    <div>
                      <span>TPM</span>
                      <b className="tenantQuotaValue">{renderLimit(t.tpm_limit, '/min')}</b>
                    </div>
                    <div>
                      <span>并发</span>
                      <b className="tenantQuotaValue">{renderLimit(t.max_concurrency)}</b>
                    </div>
                  </div>
                  <footer className="cardActions">
                    <Button size="sm" icon={<Gauge />} onClick={() => void openUsage(t)}>
                      查看用量
                    </Button>
                    <Button size="sm" icon={<Pencil />} onClick={() => setDraft(t)}>
                      编辑
                    </Button>
                    <Button size="sm" variant="danger" icon={<Trash2 />} onClick={() => void remove(t)}>
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
            description="创建租户后即可为业务系统签发 API 密钥并设置资源配额。"
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1})}>
                新增租户
              </Button>
            }
          />
        )}
      </section>

      {draft && (
        <Drawer
          size="lg"
          title={draft.id ? '编辑租户' : '新增租户'}
          subtitle="配额与限流均为 0 表示不限制；限流规则为该租户内所有 API Key 共享。"
          onClose={() => setDraft(null)}
        >
          <TenantForm
            value={draft}
            models={models}
            onSave={v => void save(v)}
            onCancel={() => setDraft(null)}
          />
        </Drawer>
      )}

      {usage && (
        <Drawer
          size="lg"
          title="租户用量"
          subtitle={usage.tenant.name}
          onClose={() => setUsage(null)}
        >
          <TenantUsageDetail tenant={usage.tenant} usage={usage.usage} />
        </Drawer>
      )}
    </div>
  )
}
