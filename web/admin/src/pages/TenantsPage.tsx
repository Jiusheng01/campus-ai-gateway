import {useState} from 'react'
import {Gauge, Pencil, Plus, Users} from 'lucide-react'
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
import {useResource} from '../lib/useResource'
import {fmt} from '../lib/format'

export function TenantsPage() {
  const toast = useToast()
  const {data, loading, error, errorStatus, reload} = useResource(async () => {
    const d = await api<{data: Tenant[]}>('/admin/tenants')
    return d.data || []
  })
  const [draft, setDraft] = useState<TenantDraft | null>(null)
  const [usage, setUsage] = useState<{tenant: Tenant; usage: TenantUsage} | null>(null)

  const save = async (v: TenantDraft) => {
    try {
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

  const quota = (n: number) => (n ? fmt(n) : '不限')
  const items = data || []

  return (
    <>
      <Panel
        title="租户管理"
        sub="租户是校园部门或业务系统的配额与凭证隔离边界。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1})}>
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
              <Table heads={['租户', '编码', '状态', '日 Token 配额', '月 Token 配额', '月请求配额', 'RPM', 'TPM', '并发', '操作']} minWidth={1200}>
                {items.map(t => (
                  <tr key={t.id}>
                    <td>
                      <b>{t.name}</b>
                    </td>
                    <td className="mono">{t.code}</td>
                    <td>
                      <StatusBadge status={t.status} />
                    </td>
                    <td className="numeric">{quota(t.daily_token_limit)}</td>
                    <td className="numeric">{quota(t.monthly_token_limit)}</td>
                    <td className="numeric">{quota(t.monthly_request_limit)}</td>
                    {/* The tenant is the rate-limit boundary (ADR-0015); these
                        three are shared by every key it owns. */}
                    <td className="numeric">{quota(t.rpm_limit)}</td>
                    <td className="numeric">{quota(t.tpm_limit)}</td>
                    <td className="numeric">{quota(t.max_concurrency)}</td>
                    <td className="rowActions">
                      <button className="link" onClick={() => void openUsage(t)}>
                        查看用量
                      </button>
                      <button className="link" onClick={() => setDraft(t)}>
                        编辑
                      </button>
                    </td>
                  </tr>
                ))}
              </Table>
            </div>

            <div className="mobileOnly cardsList">
              {items.map(t => (
                <article className="miniCard" key={t.id}>
                  <header>
                    <div>
                      <span className="eyebrow">{t.code}</span>
                      <h3>{t.name}</h3>
                    </div>
                    <StatusBadge status={t.status} />
                  </header>
                  <div className="miniCardRows">
                    <div>
                      <span>日 Token</span>
                      <b>{quota(t.daily_token_limit)}</b>
                    </div>
                    <div>
                      <span>月 Token</span>
                      <b>{quota(t.monthly_token_limit)}</b>
                    </div>
                    <div>
                      <span>月请求</span>
                      <b>{quota(t.monthly_request_limit)}</b>
                    </div>
                    <div>
                      <span>RPM</span>
                      <b>{quota(t.rpm_limit)}</b>
                    </div>
                    <div>
                      <span>TPM</span>
                      <b>{quota(t.tpm_limit)}</b>
                    </div>
                    <div>
                      <span>并发</span>
                      <b>{quota(t.max_concurrency)}</b>
                    </div>
                  </div>
                  <footer className="cardActions">
                    <Button size="sm" icon={<Gauge />} onClick={() => void openUsage(t)}>
                      查看用量
                    </Button>
                    <Button size="sm" icon={<Pencil />} onClick={() => setDraft(t)}>
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
              <Button variant="primary" icon={<Plus />} onClick={() => setDraft({status: 1})}>
                新增租户
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title={draft.id ? '编辑租户' : '新增租户'}
          subtitle="配额为 0 表示不限制，由上级配额与限流策略兜底。"
          onClose={() => setDraft(null)}
        >
          <TenantForm value={draft} onSave={v => void save(v)} onCancel={() => setDraft(null)} />
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
