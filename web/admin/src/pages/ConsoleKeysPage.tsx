import {useMemo, useState} from 'react'
import {
  Check,
  Copy,
  KeyRound,
  Layers,
  Plus,
  RefreshCw,
  Search,
  Sparkles,
  X,
} from 'lucide-react'
import {
  ApiError,
  createConsoleKey,
  getConsoleMe,
  getConsoleModels,
  listConsoleKeys,
  type ConsoleKey,
} from '../api'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {Field} from '../components/ui/Field'
import {useToast} from '../components/ui/Toast'
import {SecretModal} from '../components/keys/SecretModal'
import {useResource} from '../lib/useResource'
import {ago, copyText, fmt, num, when} from '../lib/format'

/**
 * The member's own API Keys.
 *
 * Every row comes from /api/console/keys, which binds the session's account id
 * into the query. The page never accepts an owner or a tenant from the browser:
 * the create form sends a name and a model scope, and the gateway derives the
 * rest from the session and its Tenant Membership.
 *
 * The picker is bounded by /api/console/models: the catalog is the Tenant's
 * allowlist intersected with the statically routable set, so the drawer offers
 * exactly the aliases the gateway says are callable. `/api/console/me` still
 * supplies the allowlist's shape — wildcard versus explicit — because a
 * wildcard key should keep the "including future models" meaning that a list of
 * today's aliases would lose. The same bounds are enforced server-side inside
 * the create transaction, so the picker is a convenience rather than the
 * authority.
 */
export function ConsoleKeysPage() {
  const me = useResource(getConsoleMe)
  const catalog = useResource(getConsoleModels)
  const keys = useResource(listConsoleKeys)
  const [creating, setCreating] = useState(false)
  const [secret, setSecret] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [copiedPrefix, setCopiedPrefix] = useState<string | null>(null)

  const tenantWildcard = (me.data?.allowed_models || []).includes('*')
  const availableModels = catalog.data?.models || []
  const modelAliases = availableModels.map(m => m.alias)
  const emptyReason = catalog.data?.empty_reason
  const canCreate = !!me.data?.workspace_usable && modelAliases.length > 0

  const filtered = useMemo(() => {
    const list = keys.data || []
    const q = search.trim().toLowerCase()
    return list.filter(k => {
      if (statusFilter && String(k.status) !== statusFilter) return false
      if (!q) return true
      return (
        (k.name || '').toLowerCase().includes(q) ||
        (k.key_prefix || '').toLowerCase().includes(q) ||
        (k.allowed_models || []).some(m => m.toLowerCase().includes(q))
      )
    })
  }, [keys.data, search, statusFilter])

  const copyPrefix = async (prefix: string) => {
    if (await copyText(prefix)) {
      setCopiedPrefix(prefix)
      setTimeout(() => setCopiedPrefix(null), 2000)
    }
  }

  const resetFilters = () => {
    setSearch('')
    setStatusFilter('')
    keys.reload()
  }

  const loading =
    (me.loading && !me.data) || (catalog.loading && !catalog.data) || (keys.loading && !keys.data)
  const loadError = me.error || catalog.error || keys.error

  return (
    <div className="stack">
      <section className="keysSectionCard" aria-label="我的 API Key">
        <header className="keysSectionHeader">
          <div className="keysSectionTitleBlock">
            <h2 className="keysSectionTitle">API Key</h2>
            <p className="keysSectionSub">
              这里的 Key 只属于你的账号。完整 Key 仅在创建时显示一次，之后只能看到前缀。
            </p>
          </div>
          <Button
            variant="primary"
            icon={<Plus />}
            disabled={!canCreate}
            title={
              canCreate
                ? undefined
                : me.data?.workspace_usable
                  ? '暂无可签发的模型'
                  : '请先联系管理员为你的账号分配租户'
            }
            onClick={() => setCreating(true)}
          >
            创建 API Key
          </Button>
        </header>

        {/* Filters narrow the loaded list; they issue no request. */}
        <div className="filterCard">
          <div className="filtersBar">
            <div className="searchFieldGroup">
              <Search className="searchFieldIcon" aria-hidden />
              <input
                className="filterSearchInput"
                placeholder="搜索 Key 名称、前缀或模型..."
                value={search}
                onChange={e => setSearch(e.target.value)}
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

            <div className="filterActionButtons">
              <Button size="sm" icon={<RefreshCw />} onClick={resetFilters} title="刷新并重置筛选">
                重置
              </Button>
            </div>
          </div>
        </div>

        {loadError ? (
          <ErrorState message={loadError} status={me.errorStatus || catalog.errorStatus || keys.errorStatus} onRetry={() => {
            me.reload()
            catalog.reload()
            keys.reload()
          }} />
        ) : loading ? (
          <SkeletonRows rows={5} columns={5} />
        ) : !me.data?.workspace_usable ? (
          <EmptyState
            icon={<KeyRound />}
            title="尚未分配租户"
            description="你的账号还没有加入任何租户，因此暂时不能创建 API Key。请联系管理员在「用户」中为你的账号分配租户。"
          />
        ) : emptyReason === 'tenant_allows_none' ? (
          <EmptyState
            icon={<KeyRound />}
            title="租户未开放任何模型"
            description="你的租户当前没有允许任何模型，因此不能签发可用的 API Key。请联系管理员调整租户的模型授权。"
          />
        ) : modelAliases.length === 0 ? (
          <EmptyState
            icon={<KeyRound />}
            title="暂无可调用的模型"
            description="你的租户允许的模型当前都不可调用（尚未发布或没有可用线路）。请联系管理员检查模型与线路配置。"
          />
        ) : filtered.length ? (
          <div className="keysTableWrap">
            <Table heads={['Key 名称', '允许模型', '状态', '创建时间', '最近使用']} minWidth={860}>
              {filtered.map(k => {
                const allModels = (k.allowed_models || []).includes('*')
                const models = k.allowed_models || []
                const visible = models.slice(0, 2)
                const extra = models.length - visible.length
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
                      <div className="allowedModelsChips">
                        {allModels ? (
                          <span className="modelTag wildcard">
                            <Sparkles aria-hidden /> 全部对外模型
                          </span>
                        ) : models.length ? (
                          <>
                            {visible.map(m => (
                              <span className="modelTag" key={m}>
                                {m}
                              </span>
                            ))}
                            {extra > 0 && <span className="modelTag extraBadge">+{extra}</span>}
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
                        <span className="expireDate mono">{when(k.created_at)}</span>
                      </div>
                    </td>

                    <td>
                      <div className="usageRecentCell">
                        <span className="lastUsedTime">{k.last_used_at ? ago(k.last_used_at) : '从未调用'}</span>
                        <small className="usageSummary muted">
                          累计 {num(k.usage_requests)} 次 · {fmt(k.usage_tokens)} Tokens
                        </small>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </Table>

            <footer className="tableFooterBar">
              <span className="tableRecordCount">共 {filtered.length} 条记录</span>
            </footer>
          </div>
        ) : (
          <EmptyState
            icon={<KeyRound />}
            title={keys.data?.length ? '没有匹配的 API Key' : '还没有 API Key'}
            description={
              keys.data?.length
                ? '调整搜索词或状态筛选，或重置筛选后再试。'
                : '创建你的第一个 API Key，用它连接校园模型服务。'
            }
            action={
              keys.data?.length ? undefined : (
                <Button variant="primary" icon={<Plus />} onClick={() => setCreating(true)}>
                  创建 Key
                </Button>
              )
            }
          />
        )}
      </section>

      {creating && me.data && (
        <Drawer
          size="lg"
          title="创建 API Key"
          subtitle="Key 归属于你的账号，模型范围受租户授权限制。创建后完整 Key 仅显示一次。"
          onClose={() => setCreating(false)}
        >
          <CreateKeyForm
            models={modelAliases}
            tenantWildcard={tenantWildcard}
            onCancel={() => setCreating(false)}
            onCreated={created => {
              setCreating(false)
              keys.reload()
              if (created.api_key) setSecret(created.api_key)
            }}
          />
        </Drawer>
      )}

      {secret && <SecretModal value={secret} onClose={() => setSecret(null)} />}
    </div>
  )
}

/**
 * The create form. It offers only the Tenant's allowlist: a wildcard Tenant
 * gets the explicit "all models" choice, an explicit Tenant gets its aliases,
 * and an empty Tenant never reaches this component because the page refuses to
 * open the drawer.
 */
function CreateKeyForm({
  models,
  tenantWildcard,
  onCancel,
  onCreated,
}: {
  models: string[]
  tenantWildcard: boolean
  onCancel: () => void
  onCreated: (created: ConsoleKey & {api_key: string}) => void
}) {
  const toast = useToast()
  const [name, setName] = useState('')
  const [selected, setSelected] = useState<string[]>(() => (tenantWildcard ? ['*'] : [...models]))
  const [saving, setSaving] = useState(false)

  const toggleModel = (alias: string, on: boolean) => {
    setSelected(prev => (on ? [...prev.filter(x => x !== alias), alias] : prev.filter(x => x !== alias)))
  }

  const submit = async () => {
    setSaving(true)
    try {
      const created = await createConsoleKey({
        name: name.trim(),
        allowed_models: tenantWildcard ? ['*'] : selected,
      })
      toast.success('API Key 已创建')
      onCreated(created)
    } catch (e) {
      toast.error(createKeyErrorMessage(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form
      className="keyFormStack"
      onSubmit={e => {
        e.preventDefault()
        void submit()
      }}
    >
      <fieldset className="formSectionFieldset">
        <legend className="formSectionLegend">
          <KeyRound aria-hidden /> 基础信息
        </legend>

        <Field label="名称 *" hint="便于识别的用途说明，例如 教学系统 - 生产环境。">
          <input
            required
            autoFocus
            placeholder="请输入 API Key 名称"
            value={name}
            onChange={e => setName(e.target.value)}
          />
        </Field>
      </fieldset>

      <fieldset className="formSectionFieldset">
        <legend className="formSectionLegend">
          <Layers aria-hidden /> 模型授权
        </legend>

        {tenantWildcard ? (
          <div className="wildcardActiveNotice">
            <Sparkles aria-hidden />
            <div>
              <b>租户允许全部对外模型</b>
              <p>该 Key 将获得租户当前允许的全部模型。以后租户新增的模型也会自动对该 Key 生效。</p>
            </div>
          </div>
        ) : (
          <div className="multiSelectWrapper">
            <div className="multiSelectHead">
              <span className="multiSelectTitle">允许调用的模型</span>
            </div>

            <div className="chipsContainer">
              {selected.length ? (
                selected.map(alias => (
                  <span className="modelSelectChip" key={alias}>
                    <span className="chipAlias mono">{alias}</span>
                    <button
                      type="button"
                      className="chipRemoveButton"
                      onClick={() => toggleModel(alias, false)}
                      title="移除模型"
                    >
                      <X aria-hidden />
                    </button>
                  </span>
                ))
              ) : (
                <span className="chipsEmptyPlaceholder">未选择任何模型（该 Key 将无法调用任何模型）</span>
              )}
            </div>

            <div className="addModelDropdownRow">
              <select
                className="addModelSelect"
                value=""
                onChange={e => {
                  const alias = e.target.value
                  if (alias) toggleModel(alias, true)
                }}
              >
                <option value="">+ 选择要添加的模型…</option>
                {models
                  .filter(alias => !selected.includes(alias))
                  .map(alias => (
                    <option key={alias} value={alias}>
                      {alias}
                    </option>
                  ))}
              </select>
            </div>

            <small className="fieldHint">可选项仅限你的租户允许的模型；网关会在创建时再次校验。</small>
          </div>
        )}
      </fieldset>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit" loading={saving} disabled={!name.trim()}>
          创建 API Key
        </Button>
      </div>
    </form>
  )
}

/**
 * createKeyErrorMessage turns the gateway's stable codes into an actionable
 * sentence. The limit and the offending model come from machine-readable
 * fields, not from the prose, so a server wording change cannot break this.
 */
function createKeyErrorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'tenant_not_assigned':
        return '你的账号尚未分配租户，无法创建 API Key。请联系管理员分配租户。'
      case 'tenant_models_empty':
        return '你的租户当前未开放任何模型，无法创建 API Key。请联系管理员调整租户的模型授权。'
      case 'key_limit_reached': {
        const limit = e.details.limit
        return typeof limit === 'number'
          ? `已达到个人 API Key 上限（${limit} 个）。请先删除不再使用的 Key。`
          : '已达到个人 API Key 上限。请先删除不再使用的 Key。'
      }
      case 'model_not_allowed': {
        const model = e.details.model
        return typeof model === 'string'
          ? `模型「${model}」不在你的租户允许范围内。`
          : '所选模型不在你的租户允许范围内。'
      }
      default:
        return e.message || '创建 API Key 失败'
    }
  }
  return e instanceof Error ? e.message : '创建 API Key 失败'
}
