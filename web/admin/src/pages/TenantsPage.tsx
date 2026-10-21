import {useMemo, useState} from 'react'
import {
  Gauge,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Users,
} from 'lucide-react'
import {api, type Tenant, type TenantUsage} from '../api'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {TenantForm, type TenantDraft} from '../components/tenants/TenantForm'
import {TenantUsageDetail} from '../components/tenants/TenantUsageDetail'
import {useResource} from '../lib/useResource'
import {fmt} from '../lib/format'

export function TenantsPage({onReload}: {onReload?: () => void}) {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload: localReload} = useResource(async () => {
    const d = await api<{data: Tenant[]}>('/admin/tenants')
    return d.data || []
  })

  const reload = () => {
    localReload()
    onReload?.()
  }

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

  // Clearly distinguishes "无限制" (0), "未设置" (null/undefined), and numeric values
  const renderLimit = (val?: number, unit = '') => {
    if (val === undefined || val === null) {
      return <span className="quotaUnsetTag">未设置</span>
    }
    if (val === 0) {
      return <span className="quotaUnlimitedTag">无限制</span>
    }
    return (
      <span className="quotaNumericValue mono">
        {fmt(val)} {unit && <small className="quotaUnit">{unit}</small>}
      </span>
    )
  }

  const items = data || []

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

            <label className="filterField">
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

        {loading && !data ? (
          <SkeletonRows rows={5} columns={9} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : filteredTenants.length ? (
          <div className="tenantsTableWrap">
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
                    <div className="tenantNameCell">
                      <b className="tenantNameTitle">{t.name}</b>
                    </div>
                  </td>
                  <td className="mono muted">{t.code}</td>
                  <td>
                    <span className={`statusPill ${t.status === 1 ? 'ok' : 'down'}`}>
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
                    </div>
                  </td>
                </tr>
              ))}
            </Table>

            <footer className="tableFooterBar">
              <span className="tableRecordCount">共 {filteredTenants.length} 个租户</span>
            </footer>
          </div>
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
          <TenantForm value={draft} onSave={v => void save(v)} onCancel={() => setDraft(null)} />
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
