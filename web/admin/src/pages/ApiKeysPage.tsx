import {useMemo, useState} from 'react'
import {
  Check,
  Copy,
  KeyRound,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Search,
  Sparkles,
  Trash2,
} from 'lucide-react'
import {api, deleteApiKey, type ApiKey, type ModelConfig, type Tenant} from '../api'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {KeyForm, type KeyDraft} from '../components/keys/KeyForm'
import {SecretModal} from '../components/keys/SecretModal'
import {ago, copyText, fmt, num, when} from '../lib/format'

export type ApiKeysBundle = {keys: ApiKey[]; tenants: Tenant[]; models: ModelConfig[]}

/**
 * The key table for the page's bundle.
 *
 * Every mount is inside CallsPage, which already loads keys, tenants and
 * models for the summary cards. The bundle is therefore required: a local
 * loader here would issue a second request for each of the three and, with
 * `bundle || local`, run both.
 */
export function ApiKeysPage({
  bundle,
  loading = false,
  error = '',
  errorStatus = 0,
  onReload,
}: {
  bundle: ApiKeysBundle | null
  loading?: boolean
  error?: string
  errorStatus?: number
  /** Reloads the page-level bundle; the table owns no request of its own. */
  onReload: () => void
}) {
  const toast = useToast()
  const confirm = useConfirm()
  const reload = onReload

  const [draft, setDraft] = useState<KeyDraft | null>(null)
  const [secret, setSecret] = useState<string | null>(null)
  // One row can only be doing one thing at a time; both row actions share the
  // marker so a double click cannot fire disable and delete together.
  const [busyId, setBusyId] = useState<string | null>(null)
  const [copiedPrefix, setCopiedPrefix] = useState<string | null>(null)

  // Filter state
  const [searchQuery, setSearchQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState<string>('')
  const [tenantFilter, setTenantFilter] = useState<string>('')

  const keys = bundle?.keys || []
  const tenants = bundle?.tenants || []
  const models = bundle?.models || []
  const canCreate = tenants.length > 0

  // Filter keys
  const filteredKeys = useMemo(() => {
    return keys.filter(k => {
      if (statusFilter && String(k.status) !== statusFilter) return false
      if (tenantFilter && k.tenant_id !== tenantFilter) return false
      if (searchQuery.trim()) {
        const q = searchQuery.toLowerCase().trim()
        const matchName = (k.name || '').toLowerCase().includes(q)
        const matchPrefix = (k.key_prefix || '').toLowerCase().includes(q)
        const matchTenant = (k.tenant_name || '').toLowerCase().includes(q) || (k.tenant_code || '').toLowerCase().includes(q)
        const matchModels = (k.allowed_models || []).some(m => m.toLowerCase().includes(q))
        if (!matchName && !matchPrefix && !matchTenant && !matchModels) return false
      }
      return true
    })
  }, [keys, searchQuery, statusFilter, tenantFilter])

  // A refused clipboard must say so. The operator asked for a copy; staying
  // silent would leave them believing the prefix is on it.
  const copyPrefix = async (prefix: string) => {
    if (await copyText(prefix)) {
      setCopiedPrefix(prefix)
      toast.success(`Key 前缀已复制: ${prefix}`)
      setTimeout(() => setCopiedPrefix(null), 2000)
      return
    }
    toast.error('复制失败，请手动选择并复制 Key 前缀')
  }

  const save = async (v: KeyDraft) => {
    try {
      const out = await api<{api_key?: string}>(v.id ? `/admin/api-keys/${v.id}` : '/admin/api-keys', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success(v.id ? 'API Key 已更新' : 'API Key 已创建')
      setDraft(null)
      if (out.api_key) setSecret(out.api_key)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存 API Key 失败')
    }
  }

  /**
   * Disabling is an edit, not a revocation: the credential keeps working the
   * moment the row is re-enabled, which is why it lives beside 编辑 rather than
   * behind the destructive action. The whole editable record is sent because
   * the update endpoint replaces it.
   */
  const toggleStatus = async (k: ApiKey) => {
    const next = k.status === 1 ? 2 : 1
    setBusyId(k.id)
    try {
      await api(`/admin/api-keys/${k.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: k.name,
          tenant_id: k.tenant_id,
          allowed_models: k.allowed_models || [],
          allowed_ips: k.allowed_ips || [],
          status: next,
          expire_at: k.expire_at || undefined,
        }),
      })
      toast.success(next === 2 ? `已停用「${k.name}」` : `已启用「${k.name}」`)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '更新状态失败')
    } finally {
      setBusyId(null)
    }
  }

  /**
   * Deletion removes the row, unlike revocation. A request that named this key
   * keeps only its id afterwards, which is why the confirmation asks for the
   * name to be typed.
   */
  const remove = async (k: ApiKey) => {
    const ok = await confirm({
      title: `删除「${k.name}」？`,
      description: `删除会移除这条 API Key 记录，${k.key_prefix}… 立即失效且无法恢复。历史请求日志仍会保留，但无法再把它们关联回这个 Key。如需保留可追溯性，请改用「停用」。`,
      confirmText: '确认删除',
      danger: true,
      requireText: k.name,
      requireTextLabel: `输入 Key 名称「${k.name}」以确认`,
    })
    if (!ok) return
    setBusyId(k.id)
    try {
      await deleteApiKey(k.id)
      toast.success(`已删除「${k.name}」`)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除失败')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="stack">
      {/* The list card owns its header, filters and table as one surface. */}
      <section className="keysSectionCard" aria-label="API Key 列表">
        <header className="keysSectionHeader">
          <div className="keysSectionTitleBlock">
            <h2 className="keysSectionTitle">API Key</h2>
            <p className="keysSectionSub">
              业务系统调用 /v1/* 使用的虚拟凭证。完整 Key 只在创建时显示一次，数据仅保存前缀与 HMAC 摘要。
            </p>
          </div>
          <Button
            variant="primary"
            icon={<Plus />}
            disabled={!canCreate}
            title={canCreate ? undefined : '请先创建租户'}
            onClick={() =>
              setDraft({status: 1, allowed_models: [], allowed_ips: []})
            }
          >
            + 创建 API Key
          </Button>
        </header>

        {/* Filters apply to the loaded bundle; they issue no request. */}
        <div className="filterCard">
          <div className="filtersBar">
            <div className="searchFieldGroup">
              <Search className="searchFieldIcon" aria-hidden />
              <input
                className="filterSearchInput"
                placeholder="搜索 Key 名称、租户或模型..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
              />
            </div>

            <label className="filterFieldInline">
              <span className="filterLabel">全部状态</span>
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

            <label className="filterFieldInline">
              <span className="filterLabel">全部租户</span>
              <select
                className="filterSelect"
                value={tenantFilter}
                onChange={e => setTenantFilter(e.target.value)}
              >
                <option value="">全部租户</option>
                {tenants.map(t => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            </label>

            <div className="filterActionButtons">
              <Button
                size="sm"
                icon={<RefreshCw />}
                onClick={() => {
                  setSearchQuery('')
                  setStatusFilter('')
                  setTenantFilter('')
                  reload()
                }}
                title="刷新并重置筛选"
              >
                重置
              </Button>
            </div>
          </div>
        </div>

        {loading && !bundle ? (
          <SkeletonRows rows={6} columns={7} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : filteredKeys.length ? (
          <div className="keysTableWrap">
            <Table
              heads={[
                'Key 名称',
                '所属租户',
                '允许模型',
                '状态',
                '过期时间',
                '最近调用',
                '操作',
              ]}
              minWidth={1080}
            >
              {filteredKeys.map(k => {
                const isAllModels = (k.allowed_models || []).includes('*')
                const modelsList = k.allowed_models || []
                const visibleModels = modelsList.slice(0, 2)
                const extraCount = modelsList.length - visibleModels.length

                return (
                  <tr key={k.id}>
                    <td>
                      <div className="keyNameCell">
                        <b className="keyNameTitle">{k.name}</b>
                        <div className="keyPrefixRow">
                          <code className="keyPrefixCode mono">{k.key_prefix}…</code>
                          <button
                            type="button"
                            className="keyCopyPrefixBtn"
                            onClick={() => void copyPrefix(k.key_prefix)}
                            title="复制 Key 前缀"
                          >
                            {copiedPrefix === k.key_prefix ? (
                              <Check className="copySuccessIcon" aria-hidden />
                            ) : (
                              <Copy aria-hidden />
                            )}
                          </button>
                        </div>
                      </div>
                    </td>

                    <td>
                      <div className="tenantCell">
                        {/* The tenant is the key's owner. If the response carries
                            no name, say that instead of inventing a default:
                            a made-up owner sends an operator to the wrong
                            department when a request is refused. */}
                        {k.tenant_name ? (
                          <span className="tenantName">{k.tenant_name}</span>
                        ) : (
                          <span className="tenantName unknown">归属租户未返回</span>
                        )}
                        {k.tenant_code && (
                          <small className="tenantCode mono muted"> / {k.tenant_code}</small>
                        )}
                      </div>
                    </td>

                    <td>
                      <div className="allowedModelsChips">
                        {isAllModels ? (
                          <span className="modelTag wildcard">
                            <Sparkles aria-hidden /> 全部对外模型
                          </span>
                        ) : modelsList.length ? (
                          <>
                            {visibleModels.map(m => (
                              <span className="modelTag" key={m}>
                                {m}
                              </span>
                            ))}
                            {extraCount > 0 && (
                              <span className="modelTag extraBadge">+{extraCount}</span>
                            )}
                          </>
                        ) : (
                          <span className="muted">未授权任何模型</span>
                        )}
                      </div>
                    </td>

                    <td>
                      <span className={`statusChip ${k.status === 1 ? 'ok' : 'down'}`}>
                        <i className={`dot ${k.status === 1 ? 'on' : 'down'}`} aria-hidden />
                        {k.status === 1 ? '启用' : '停用'}
                      </span>
                    </td>

                    <td>
                      <div className="expireCell">
                        {k.expire_at ? (
                          // Shared formatter: a timestamp reads the same here as
                          // on every other page.
                          <span className="expireDate mono">{when(k.expire_at)}</span>
                        ) : (
                          <span className="permanentTag">永久有效</span>
                        )}
                      </div>
                    </td>

                    <td>
                      <div className="usageRecentCell">
                        <span className="lastUsedTime">
                          {k.last_used_at ? ago(k.last_used_at) : '从未调用'}
                        </span>
                        <small className="usageSummary muted">
                          {/* Lifetime totals for this key, not a period figure. */}
                          累计 {num(k.usage_requests)} 次 · {fmt(k.usage_tokens)} Tokens
                        </small>
                      </div>
                    </td>

                    <td>
                      <div className="rowActions">
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => setDraft(k)}
                        >
                          <Pencil aria-hidden />
                          编辑
                        </button>
                        <button
                          type="button"
                          className="tableActionLink"
                          disabled={busyId === k.id}
                          onClick={() => void toggleStatus(k)}
                        >
                          <Power aria-hidden />
                          {busyId === k.id ? '处理中…' : k.status === 1 ? '停用' : '启用'}
                        </button>
                        <button
                          type="button"
                          className="tableActionLink dangerText"
                          disabled={busyId === k.id}
                          onClick={() => void remove(k)}
                        >
                          <Trash2 aria-hidden />
                          {busyId === k.id ? '处理中…' : '删除'}
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </Table>

            <footer className="tableFooterBar">
              <span className="tableRecordCount">共 {filteredKeys.length} 条记录</span>
            </footer>
          </div>
        ) : (
          <EmptyState
            icon={<KeyRound />}
            title="暂无匹配的 API Key"
            description={
              canCreate
                ? '创建 Key 后，校园各业务系统即可调用受控对外模型。'
                : '请先在「租户配额」页面创建租户。'
            }
            action={
              canCreate ? (
                <Button
                  variant="primary"
                  icon={<Plus />}
                  onClick={() =>
                    setDraft({status: 1, allowed_models: [], allowed_ips: []})
                  }
                >
                  创建 Key
                </Button>
              ) : undefined
            }
          />
        )}
      </section>

      {/* Create/edit happens in a slide-over so the list stays in view. */}
      {draft && (
        <Drawer
          size="lg"
          title={draft.id ? '编辑 API Key' : '创建 API Key'}
          subtitle="为租户创建新的 API Key，创建后仅显示一次完整 Key。"
          onClose={() => setDraft(null)}
        >
          <KeyForm
            value={draft}
            tenants={tenants}
            models={models}
            onSave={v => void save(v)}
            onCancel={() => setDraft(null)}
          />
        </Drawer>
      )}

      {/* Secret Reveal Modal */}
      {secret && <SecretModal value={secret} onClose={() => setSecret(null)} />}
    </div>
  )
}
