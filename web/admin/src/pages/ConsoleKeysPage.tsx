import {useMemo, useState} from 'react'
import {
  Check,
  Copy,
  KeyRound,
  Layers,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Search,
  Shield,
  Sparkles,
  Trash2,
} from 'lucide-react'
import {
  ApiError,
  createConsoleKey,
  deleteConsoleKey,
  getConsoleMe,
  getConsoleModels,
  listConsoleKeys,
  setConsoleKeyStatus,
  updateConsoleKey,
  type ConsoleKey,
  type ConsoleKeyUpdate,
} from '../api'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {Field} from '../components/ui/Field'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {SecretModal} from '../components/keys/SecretModal'
import {ModelScopePicker} from '../components/models/ModelScopePicker'
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
  const toast = useToast()
  const confirm = useConfirm()
  const me = useResource(getConsoleMe)
  const catalog = useResource(getConsoleModels)
  const keys = useResource(listConsoleKeys)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ConsoleKey | null>(null)
  const [secret, setSecret] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [copiedPrefix, setCopiedPrefix] = useState<string | null>(null)

  const tenantWildcard = (me.data?.allowed_models || []).includes('*')
  // The Tenant's named allowlist, used to expand a stored "*" key when the
  // Tenant itself is no longer wildcard. The catalog below is the callable
  // subset offered by the picker; the Tenant list is the scope an edit is
  // allowed to store, so the edit form needs both.
  const tenantModels = (me.data?.allowed_models || []).filter(m => m !== '*')
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

  // A refused clipboard must say so; silence would leave the operator thinking
  // the prefix is on the clipboard, which is the same mistake the admin key
  // list already fixed.
  const copyPrefix = async (prefix: string) => {
    if (await copyText(prefix)) {
      setCopiedPrefix(prefix)
      toast.success(`Key 前缀已复制: ${prefix}`)
      setTimeout(() => setCopiedPrefix(null), 2000)
      return
    }
    toast.error('复制失败，请手动选择并复制 Key 前缀')
  }

  const resetFilters = () => {
    setSearch('')
    setStatusFilter('')
    keys.reload()
  }

  /**
   * Pausing is reversible and keeps the credential, so it needs no typed
   * confirmation — the action itself says which way it will go, and the toast
   * states what remains true afterwards. It is a dedicated status call rather
   * than an edit, so it cannot carry a rename or a model change with it.
   */
  const toggleStatus = async (k: ConsoleKey) => {
    const next = k.status === 1 ? 2 : 1
    setBusyId(k.id)
    try {
      await setConsoleKeyStatus(k.id, next)
      toast.success(
        next === 2
          ? `已停用「${k.name}」：凭据仍然保留，重新启用即可恢复使用`
          : `已启用「${k.name}」：凭据与停用前相同`,
      )
      keys.reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '更新状态失败')
    } finally {
      setBusyId(null)
    }
  }

  /**
   * Deletion is permanent: the row and the credential are gone. The typed name
   * is the cost of telling this apart from the reversible pause beside it.
   * Request Records survive, because they carry the owner snapshot.
   */
  const remove = async (k: ConsoleKey) => {
    const ok = await confirm({
      title: `删除「${k.name}」？`,
      description: `删除会永久移除这条 Key 及其凭据，${k.key_prefix}… 将立即失效且无法恢复。已产生的请求记录仍会保留并归属于你的账号。若只是想暂停调用并保留凭据，请改用「停用」。`,
      confirmText: '确认删除',
      danger: true,
      requireText: k.name,
      requireTextLabel: `输入 Key 名称「${k.name}」以确认`,
    })
    if (!ok) return
    setBusyId(k.id)
    try {
      await deleteConsoleKey(k.id)
      toast.success(`已删除「${k.name}」：凭据已移除`)
      if (editing?.id === k.id) setEditing(null)
      keys.reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除失败')
    } finally {
      setBusyId(null)
    }
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
            <Table
              heads={['Key 名称', '允许模型', '状态', '创建时间', '最近使用', '操作']}
              minWidth={1020}
            >
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

                    <td>
                      <div className="rowActions">
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => setEditing(k)}
                        >
                          <Pencil aria-hidden />
                          编辑
                        </button>
                        <button
                          type="button"
                          className="tableActionLink"
                          disabled={busyId === k.id}
                          onClick={() => void toggleStatus(k)}
                          title={
                            k.status === 1
                              ? '暂停调用，但保留凭据，之后可重新启用'
                              : '用同一凭据恢复调用'
                          }
                        >
                          <Power aria-hidden />
                          {busyId === k.id ? '处理中…' : k.status === 1 ? '停用' : '启用'}
                        </button>
                        <button
                          type="button"
                          className="tableActionLink dangerText"
                          disabled={busyId === k.id}
                          onClick={() => void remove(k)}
                          title="永久删除这条 Key 及其凭据"
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
          subtitle="个人 API Key，模型范围受租户授权限制。提交后完整 Key 仅显示一次。"
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

      {editing && (
        <Drawer
          size="lg"
          title="编辑 API Key"
          subtitle="编辑名称、模型范围、IP 白名单与过期时间。"
          onClose={() => setEditing(null)}
        >
          <EditKeyForm
            key={editing.id}
            row={editing}
            models={modelAliases}
            tenantModels={tenantModels}
            tenantWildcard={tenantWildcard}
            onCancel={() => setEditing(null)}
            onSaved={() => {
              setEditing(null)
              keys.reload()
            }}
          />
        </Drawer>
      )}

      {secret && <SecretModal value={secret} onClose={() => setSecret(null)} />}
    </div>
  )
}

/**
 * The create form. A wildcard Tenant may issue either the wildcard (which
 * keeps including models added later) or a narrower explicit selection; an
 * explicit Tenant offers only its aliases. The picker itself never invents a
 * scope: it starts at the Tenant's most permissive valid choice and the
 * operator narrows it deliberately.
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
  // Under a wildcard Tenant the form starts unrestricted; under an explicit
  // Tenant it starts with the whole callable set selected, which is the same
  // reach the old always-send-the-list behaviour had, but now visible.
  const [allModels, setAllModels] = useState(tenantWildcard)
  const [selected, setSelected] = useState<string[]>(() => (tenantWildcard ? [] : [...models]))
  const [saving, setSaving] = useState(false)

  const scope = tenantWildcard && allModels ? ['*'] : selected

  const submit = async () => {
    setSaving(true)
    try {
      const created = await createConsoleKey({
        name: name.trim(),
        allowed_models: scope,
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

        <ModelScopePicker
          value={scope}
          onChange={next => {
            if (next.includes('*')) {
              setAllModels(true)
              return
            }
            setAllModels(false)
            setSelected(next)
          }}
          options={models.map(alias => ({alias}))}
          allowWildcard={tenantWildcard}
          title="允许调用的模型"
          wildcardLabel="全部对外模型（含以后新增）"
          wildcardNoticeTitle="租户允许全部对外模型"
          wildcardNoticeText="该 Key 将获得租户当前允许的全部模型。以后租户新增的模型也会自动对该 Key 生效。"
          emptyPlaceholder="未选择任何模型（该 Key 将无法调用任何模型）"
          addPlaceholder="+ 选择要添加的模型…"
          hint="可选项仅限你的租户允许的模型；网关会在创建时再次校验。"
        />
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
 * The edit form. It offers the same Tenant-bounded model picker as creation,
 * plus the network and expiry fields the edit endpoint accepts.
 *
 * A wildcard Tenant may keep the wildcard — which continues to include models
 * added later — or narrow the key to explicit aliases. An explicit Tenant can
 * only choose from its list, and the server applies the same bound again.
 *
 * The form never sends tenant_id, owner_user_id or status: those are absent
 * from ConsoleKeyUpdate, and status has its own row action.
 */
function EditKeyForm({
  row,
  models,
  tenantModels,
  tenantWildcard,
  onCancel,
  onSaved,
}: {
  row: ConsoleKey
  models: string[]
  /** The Tenant's named allowlist, used to expand a stored "*" key. */
  tenantModels: string[]
  tenantWildcard: boolean
  onCancel: () => void
  onSaved: () => void
}) {
  const toast = useToast()
  const stored = row.allowed_models || []
  const storedWildcard = stored.includes('*')
  const storedExplicit = stored.filter(m => m !== '*')
  // A "*" key under an explicit Tenant is the dangerous case: the stored value
  // includes models the Tenant no longer grants, and the server expands a
  // submitted "*" against the current Tenant list. Show that resolved list
  // rather than an empty selection, so saving a rename cannot silently decode
  // to deny-all. The operator can remove chips if they really mean to narrow.
  const resolvedFromWildcard = storedWildcard && !tenantWildcard
  const [name, setName] = useState(row.name)
  const [allModels, setAllModels] = useState(storedWildcard && tenantWildcard)
  const [selected, setSelected] = useState<string[]>(() => {
    if (!storedWildcard) return storedExplicit
    return tenantWildcard ? [] : [...tenantModels]
  })
  const [ips, setIps] = useState((row.allowed_ips || []).join('\n'))
  const [expireAt, setExpireAt] = useState(row.expire_at || '')
  const [saving, setSaving] = useState(false)

  // The dropdown offers the callable catalog plus any alias already stored or
  // granted by the Tenant, so a resolved or stale scope stays visible and can
  // be removed deliberately instead of disappearing from the form.
  const optionAliases = Array.from(new Set([...models, ...tenantModels, ...storedExplicit]))

  const submit = async () => {
    const trimmed = name.trim()
    if (!trimmed) {
      toast.error('Key 名称不能为空')
      return
    }

    // The API takes an instant, not a local wall clock. Parsing here and
    // sending ISO keeps the browser's timezone from changing the meaning of a
    // date the user typed.
    let expireValue: string | null = null
    const rawExpire = expireAt.trim()
    if (rawExpire) {
      const parsed = new Date(rawExpire)
      if (Number.isNaN(parsed.getTime())) {
        toast.error('过期时间格式无效，请填写 ISO 8601 时间（例如 2027-01-01T00:00:00Z）或留空')
        return
      }
      expireValue = parsed.toISOString()
    }

    const input: ConsoleKeyUpdate = {
      name: trimmed,
      allowed_models: tenantWildcard && allModels ? ['*'] : selected,
      allowed_ips: ips
        .split(/\n|,/)
        .map(x => x.trim())
        .filter(Boolean),
      expire_at: expireValue,
    }

    setSaving(true)
    try {
      await updateConsoleKey(row.id, input)
      toast.success('API Key 已更新')
      onSaved()
    } catch (e) {
      toast.error(createKeyErrorMessage(e, '保存 API Key 失败'))
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

        {resolvedFromWildcard && (
          <p className="hint">
            该 Key 原先允许全部模型，但租户的模型授权已经收窄。这里展示的是租户当前允许的模型，
            保存后会按这份清单记录；如需进一步缩小范围，请移除对应模型。
          </p>
        )}

        <ModelScopePicker
          value={tenantWildcard && allModels ? ['*'] : selected}
          onChange={next => {
            if (next.includes('*')) {
              setAllModels(true)
              return
            }
            setAllModels(false)
            setSelected(next)
          }}
          options={optionAliases.map(alias => ({alias}))}
          allowWildcard={tenantWildcard}
          title="允许调用的模型"
          wildcardLabel="全部对外模型（含以后新增）"
          wildcardNoticeTitle="租户允许全部对外模型"
          wildcardNoticeText="该 Key 将继续获得租户允许的全部模型，包括以后新增的模型。"
          emptyPlaceholder="未选择任何模型（该 Key 将无法调用任何模型）"
          addPlaceholder="+ 选择要添加的模型…"
          hint="可选项仅限你的租户允许的模型；网关会在保存时再次校验。"
        />
      </fieldset>

      <fieldset className="formSectionFieldset">
        <legend className="formSectionLegend">
          <Shield aria-hidden /> 网络与有效期
        </legend>

        <Field
          label="允许 IP / CIDR 白名单"
          hint="每行一个，例如 192.168.1.0/24。留空表示不限制客户端来源地址。"
        >
          <textarea
            rows={3}
            placeholder="例如: 192.168.1.0/24，每行一个"
            value={ips}
            onChange={e => setIps(e.target.value)}
          />
        </Field>

        <Field
          label="过期时间"
          hint="留空表示永久有效。支持 ISO 8601 格式，例如 2026-12-31T23:59:59Z。"
        >
          <input
            className="mono"
            placeholder="永久有效 (留空)"
            value={expireAt}
            onChange={e => setExpireAt(e.target.value)}
          />
        </Field>
      </fieldset>

      <div className="formActions">
        <Button onClick={onCancel}>取消</Button>
        <Button variant="primary" type="submit" loading={saving} disabled={!name.trim()}>
          保存修改
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
function createKeyErrorMessage(e: unknown, fallback = '创建 API Key 失败'): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'tenant_not_assigned':
        return '你的账号尚未分配租户，无法创建或修改 API Key。请联系管理员分配租户。'
      case 'tenant_models_empty':
        return '你的租户当前未开放任何模型，无法创建或修改 API Key。请联系管理员调整租户的模型授权。'
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
      case 'api_key_not_found':
        return '这条 API Key 已不存在，可能已被删除。'
      default:
        return e.message || fallback
    }
  }
  return e instanceof Error ? e.message : fallback
}
