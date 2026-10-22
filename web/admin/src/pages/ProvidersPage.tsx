import {useCallback, useMemo, useState} from 'react'
import {AlertCircle, CheckCircle2, Network, Plus} from 'lucide-react'
import {
  api,
  type CircuitView,
  type DashboardStatus,
  type Provider,
  type ProviderDeleteResult,
  type ProviderImpact,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Table} from '../components/ui/Table'
import {Drawer} from '../components/ui/Drawer'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ProviderForm, type ProviderDraft} from '../components/providers/ProviderForm'
import {ProviderTestPanel} from '../components/providers/ProviderTestPanel'
import {ProviderModelsPanel} from '../components/providers/ProviderModelsPanel'
import {RuntimeSummaryPanel, type LogSeed} from '../components/models/RuntimeSummaryPanel'
import {providerTemplates, PROTOCOLS, protocolLabel} from '../components/providers/templates'
import {Pagination} from '../components/ui/Pagination'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard} from '../components/ui/StatCard'
import {Detail} from '../components/ui/Detail'
import {useResource} from '../lib/useResource'
import {ago, ms, ratio, when} from '../lib/format'

const PAGE_SIZE = 15

type Filters = {name: string; provider_type: string; protocol: string; status: string}
const emptyFilters: Filters = {name: '', provider_type: '', protocol: '', status: ''}

type Tab = 'details' | 'basic' | 'models' | 'test' | 'usage'

type Bundle = {
  providers: Provider[]
  /** How many channels match the table's current filters; the pager's denominator. */
  total: number
  /**
   * The whole configuration, not the filtered page. The summary cards describe
   * every channel, so their counts must not move when the operator narrows the
   * table. `null` means the status request failed and there is no number to
   * show — which is different from zero.
   */
  counts: NonNullable<DashboardStatus['config_counts']> | null
  /**
   * Provider Circuit state per channel. It lives in this gateway process only,
   * so it is an instance-scoped observation rather than a stored verdict.
   * `null` means /admin/status was unavailable, not "no circuits".
   */
  circuits: CircuitView[] | null
  sampledAt: Date
}

async function deleteImpact(providerID: string): Promise<ProviderImpact> {
  return api<ProviderImpact>(`/admin/providers/${providerID}/impact`)
}

/**
 * A Provider Circuit is bucketed by Provider name in this process's breaker,
 * so the row is matched by name rather than by id.
 */
function circuitTone(circuit: CircuitView | undefined, unavailable: boolean) {
  if (unavailable) return 'unknown'
  if (!circuit) return 'none'
  if (circuit.state === 'open') return 'open'
  if (circuit.state === 'half-open') return 'half-open'
  return 'closed'
}

/**
 * Circuit state is an observation of this gateway instance, not a stored health
 * verdict: the breaker lives in memory and resets with the process. The label
 * therefore names the instance and prints the state verbatim, never "healthy".
 */
function CircuitChip({
  circuit,
  unavailable,
  sampledAt,
}: {
  circuit: CircuitView | undefined
  unavailable: boolean
  sampledAt?: Date
}) {
  const tone = circuitTone(circuit, unavailable)
  const state = unavailable ? '未知' : circuit ? circuit.state : '未观测'
  return (
    <span
      className={`circuitChip ${tone}`}
      title={sampledAt ? `本实例内存状态，采样于 ${sampledAt.toLocaleTimeString()}` : '本实例内存状态'}
    >
      <span className="circuitScope">本实例熔断</span>
      <b className="circuitValue">{state}</b>
      {circuit && circuit.retry_in_ms > 0 && (
        <span className="circuitRetry">{Math.ceil(circuit.retry_in_ms / 1000)} 秒后探测</span>
      )}
    </span>
  )
}

function credentialText(p: Provider): string {
  const credential = p.credential
  if (!credential) return '未配置'
  if (credential.source_type === 'env') {
    return `环境变量 ${p.credential_env || '—'}${credential.configured ? '（已就绪）' : '（未读取到值）'}`
  }
  if (credential.source_type === 'inline') {
    return `${credential.configured ? '内联凭据已配置' : '内联凭据未就绪'}${credential.last4 ? ` · 尾号 ${credential.last4}` : ''}`
  }
  return credential.source_type || '—'
}

function lastTestText(p: Provider): string | null {
  const test = p.last_test
  if (!test) return '尚未诊断'
  const upstream = test.upstream_model ? ` · ${test.upstream_model}` : ''
  const error = test.error_kind ? ` · ${test.error_kind}` : ''
  return `${test.ok ? '成功' : '失败'} · HTTP ${test.status_code} · ${ms(test.latency_ms)} · ${when(test.at)}${upstream}${error}`
}

/**
 * The read-only facts an operator needs before disabling or deleting a channel:
 * what depends on it, how its last diagnostic went, and this instance's circuit.
 */
function ChannelFacts({
  provider,
  circuit,
  unavailable,
  sampledAt,
}: {
  provider: Provider
  circuit: CircuitView | undefined
  unavailable: boolean
  sampledAt?: Date
}) {
  return (
    <Detail
      title="渠道事实"
      rows={[
        ['配置状态', provider.status === 1 ? '已启用；不代表上游健康' : '已停用'],
        ['凭据', credentialText(provider)],
        ['候选模型', provider.candidate_model_count == null ? null : `${provider.candidate_model_count} 个`],
        ['引用线路', provider.referenced_route_count == null ? null : `${provider.referenced_route_count} 条`],
        ['关联对外模型', provider.referenced_model_count == null ? null : `${provider.referenced_model_count} 个`],
        ['最近一次诊断', lastTestText(provider)],
        [
          '实例熔断',
          <span key="circuit" className="channelFactCircuit">
            <CircuitChip circuit={circuit} unavailable={unavailable} sampledAt={sampledAt} />
            <span className="muted">
              {circuit ? ` · ${circuit.failures} 次连续失败` : ''}
              {sampledAt ? `（本实例内存，采样于 ${sampledAt.toLocaleTimeString()}）` : ''}
            </span>
          </span>,
        ],
      ]}
    />
  )
}

export function ProvidersPage({
  canPreviewContent = false,
  canOpenRawLogs = false,
  onOpenLogs,
}: {
  canPreviewContent?: boolean
  canOpenRawLogs?: boolean
  onOpenLogs?: (filters: LogSeed) => void
}) {
  const toast = useToast()
  const confirm = useConfirm()
  const [draftFilters, setDraftFilters] = useState<Filters>(emptyFilters)
  const [filters, setFilters] = useState<Filters>(emptyFilters)
  const [offset, setOffset] = useState(0)
  const [draft, setDraft] = useState<ProviderDraft | null>(null)
  const [tab, setTab] = useState<Tab>('basic')
  const [deleting, setDeleting] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batching, setBatching] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [changingStatus, setChangingStatus] = useState(false)

  const query = useMemo(() => {
    const q = new URLSearchParams()
    if (filters.name.trim()) q.set('name', filters.name.trim())
    if (filters.provider_type.trim()) q.set('provider_type', filters.provider_type.trim())
    if (filters.protocol) q.set('protocol', filters.protocol)
    if (filters.status) q.set('status', filters.status)
    q.set('limit', String(PAGE_SIZE))
    q.set('offset', String(offset))
    return q.toString()
  }, [filters, offset])

  const loader = useCallback(async (): Promise<Bundle> => {
    const [p, status] = await Promise.all([
      api<{data: Provider[]; total?: number}>(`/admin/providers?${query}`),
      api<DashboardStatus>('/admin/status').catch(() => null),
    ])
    return {
      providers: p.data || [],
      total: p.total ?? (p.data || []).length,
      counts: status?.config_counts ?? null,
      circuits: status ? status.circuits ?? [] : null,
      sampledAt: new Date(),
    }
  }, [query])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [query])

  const save = async (v: ProviderDraft) => {
    if (saving) return
    setSaving(true)
    setSaveError('')
    try {
      await api(v.id ? `/admin/providers/${v.id}` : '/admin/providers', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success(v.id ? '渠道配置已更新' : '渠道已创建')
      setDraft(null)
      reload()
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : '保存渠道失败，请重试。')
    } finally {
      setSaving(false)
    }
  }

  const remove = async (p: Provider) => {
    if (deleting) return
    setDeleting(p.id)
    try {
      const impact = await deleteImpact(p.id)
      const ok = await confirm({
        title: `删除渠道 ${p.name}？`,
        description: <>
          <span>将永久删除该渠道、{impact.routes} 条线路和 {impact.provider_models} 个候选模型；其中 {impact.orphaned_models} 个对外模型会因失去全部线路被级联删除。</span>
          <span> 共影响 {impact.affected_models.length} 个对外模型：{impact.affected_models.length ? impact.affected_models.slice(0, 5).map(m => `${m.alias}（${m.orphaned ? '删除' : `保留 ${m.remaining_routes} 条线路`}）`).join('、') : '无'}{impact.affected_models.length > 5 ? '等' : ''}。</span>
          <span> API Key 中已有的模型授权不会自动改写。提交时重新校验，实际删除数量以返回结果为准。此操作不可撤销。</span>
        </>,
        confirmText: '永久删除',
        danger: true,
        requireText: p.name,
        requireTextLabel: `输入渠道名 ${p.name} 以确认`,
      })
      if (!ok) return
      const result = await api<ProviderDeleteResult>(`/admin/providers/${encodeURIComponent(p.id)}`, {method: 'DELETE'})
      toast.success(
        `已删除渠道：${result.deleted_routes} 条线路、${result.deleted_virtual_models} 个对外模型、${result.deleted_provider_models} 条模型清单`,
      )
      setSelected(previous => new Set([...previous].filter(id => id !== p.id)))
      if (draft?.id === p.id) setDraft(null)
      if (items.length === 1 && offset > 0) setOffset(Math.max(0, offset - PAGE_SIZE))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法完成删除，请重试。')
    } finally {
      setDeleting(null)
    }
  }

  const toggleSelected = (id: string) => {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else if (next.size < 50) next.add(id)
      return next
    })
  }

  const toggleAll = () => {
    const items = data?.providers || []
    if (selected.size === items.length) {
      setSelected(new Set())
    } else {
      setSelected(new Set(items.map(p => p.id)))
    }
  }

  const batchStatus = async (status: 1 | 2) => {
    if (!selected.size) return
    if (status === 2 && !await confirm({title: `停用选中的 ${selected.size} 个渠道？`, description: '停用后不再通过这些渠道发起新的模型请求，配置、候选模型和线路会保留。其他线路是否可用需另行检查。', confirmText: '确认停用', danger: true})) return
    setBatching(true)
    const results: {name: string; ok: boolean}[] = []
    for (const p of data?.providers || []) {
      if (!selected.has(p.id)) continue
      try {
        await api(`/admin/providers/${p.id}`, {method: 'PATCH', body: JSON.stringify({status})})
        results.push({name: p.name, ok: true})
      } catch {
        results.push({name: p.name, ok: false})
      }
    }
    setBatching(false)
    const failed = results.filter(r => !r.ok)
    if (failed.length) {
      toast.error(`${results.length - failed.length} 个成功，${failed.length} 个失败：${failed.map(f => f.name).join('、')}`)
    } else {
      toast.success(`${results.length} 个渠道已${status === 1 ? '启用' : '停用'}`)
    }
    setSelected(new Set())
    reload()
  }

  const items = data?.providers || []
  const total = data?.total || 0

  // The summary cards describe the whole configuration, so they use the
  // unfiltered counts the status endpoint computes over every channel. The
  // table's `total` is the filter's denominator and must not feed these cards:
  // "在线 3" over nine channels cannot become "在线 1" because a name filter
  // left one row on screen. When the status request failed there is no count to
  // show, and a dash is the honest rendering rather than zero.
  const channelTotal = data?.counts?.providers ?? null
  const onlineCount = data?.counts?.enabled_providers ?? null
  const offlineCount = channelTotal !== null && onlineCount !== null ? channelTotal - onlineCount : null
  // One percentage formatter across the console: provider shares rounded
  // locally drifted from the call-management and overview figures.
  const onlinePct = channelTotal ? ratio(onlineCount ?? 0, channelTotal) : null
  const offlinePct = channelTotal ? ratio(offlineCount ?? 0, channelTotal) : null

  const circuits = useMemo(
    () => new Map((data?.circuits ?? []).map(c => [c.provider, c])),
    [data?.circuits],
  )

  const [openedProvider, setActiveProvider] = useState<Provider | null>(null)
  const activeProvider = items.find(p => p.id === openedProvider?.id) ?? openedProvider

  const openDrawer = (p: Provider, defaultTab: Tab = 'details') => {
    setSaveError('')
    setTab(defaultTab)
    setActiveProvider(p)
    setDraft({
      id: p.id,
      name: p.name,
      base_url: p.base_url,
      protocol: p.protocol,
      provider_type: p.provider_type,
      description: p.description,
      credential_env: p.credential_env,
      credential: p.credential,
      status: p.status,
    })
  }

  return (
    <div className="stack">
      <PageBanner
        title="渠道管理"
        subtitle="维护模型服务的连接配置、凭据与候选模型。启用状态与最近诊断分别展示。"
      />

      <section aria-label="渠道概览统计" className="providersSummarySection">
        <div className="providersSummaryHeader">
          <div className="providersSummaryStats">
            <StatCard
              label="渠道总数"
              value={channelTotal ?? '—'}
              icon={<Network />}
              tone="default"
              className="compactStat"
            />
            <StatCard
              label="已启用"
              value={onlineCount ?? '—'}
              subtext={onlinePct === null ? undefined : `(${onlinePct})`}
              icon={<CheckCircle2 />}
              tone="success"
              className="compactStat"
            />
            <StatCard
              label="已停用"
              value={offlineCount ?? '—'}
              subtext={offlinePct === null ? undefined : `(${offlinePct})`}
              icon={<AlertCircle />}
              tone={offlineCount ? 'warning' : 'default'}
              className="compactStat"
            />
          </div>

          <div className="providersSummaryAction">
            <Button
              variant="primary"
              icon={<Plus />}
              onClick={() => {
                setSaveError('')
                setTab('basic')
                setDraft({status: 1, protocol: 'openai-chat'})
              }}
            >
              新建渠道
            </Button>
          </div>
        </div>
      </section>

      <div className="filterCard">
        <div className="filtersBar">
          <label className="filterFieldInline">
            <span className="filterLabel">名称</span>
            <input
              className="filterInput"
              placeholder="请输入渠道名称"
              value={draftFilters.name}
              onChange={e => setDraftFilters({...draftFilters, name: e.target.value})}
              onKeyDown={e => {
                if (e.key === 'Enter') {
                  setFilters(draftFilters)
                  setOffset(0)
                  setSelected(new Set())
                }
              }}
            />
          </label>

          <label className="filterFieldInline">
            <span className="filterLabel">供应商</span>
            <select
              className="filterSelect"
              value={draftFilters.provider_type}
              onChange={e => setDraftFilters({...draftFilters, provider_type: e.target.value})}
            >
              <option value="">全部</option>
              {providerTemplates.filter(t => t.id !== 'custom').map(t => <option key={t.id} value={t.id}>{t.label}</option>)}
              <option value="custom">自定义 / 实验室</option>
            </select>
          </label>

          <label className="filterFieldInline">
            <span className="filterLabel">协议</span>
            <select
              className="filterSelect"
              value={draftFilters.protocol}
              onChange={e => setDraftFilters({...draftFilters, protocol: e.target.value})}
            >
              <option value="">全部</option>
              {PROTOCOLS.map(p => (
                <option key={p} value={p}>
                  {protocolLabel(p)}
                </option>
              ))}
            </select>
          </label>

          <label className="filterFieldInline">
            <span className="filterLabel">状态</span>
            <select
              className="filterSelect"
              value={draftFilters.status}
              onChange={e => setDraftFilters({...draftFilters, status: e.target.value})}
            >
              <option value="">全部</option>
              <option value="1">已启用</option>
              <option value="2">停用</option>
            </select>
          </label>

          <div className="filterActionButtons">
            <Button
              onClick={() => {
                setFilters(emptyFilters)
                setDraftFilters(emptyFilters)
                setOffset(0)
                setSelected(new Set())
              }}
            >
              重置
            </Button>
            <Button
              variant="primary"
              onClick={() => {
                setFilters(draftFilters)
                setOffset(0)
                setSelected(new Set())
              }}
            >
              筛选
            </Button>
          </div>
        </div>
      </div>

      {/* Batch Selection Bar */}
      {selected.size > 0 && (
        <div className="batchActionBar">
          <span className="batchSelectionCount">
            已选择 <b>{selected.size}</b> 个渠道（最多 50 个）
          </span>
          <div className="batchButtons">
            <Button size="sm" loading={batching} onClick={() => void batchStatus(1)}>
              批量启用
            </Button>
            <Button size="sm" variant="danger" loading={batching} onClick={() => void batchStatus(2)}>
              批量停用
            </Button>
          </div>
        </div>
      )}

      <Panel title="渠道列表" sub={`当前筛选共 ${total} 个渠道；上方统计为全部配置，不代表实时健康。`}>
        {loading && !data ? (
          <SkeletonRows rows={5} columns={9} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : items.length ? (
          <>
            <Table
              heads={[
                <input
                  key="check-all"
                  type="checkbox"
                  aria-label="全选本页渠道"
                  checked={items.length > 0 && selected.size === items.length}
                  onChange={toggleAll}
                />,
                '渠道名称',
                '供应商',
                '连接',
                '状态',
                '凭据状态',
                '最近诊断',
                '候选模型 / 引用',
                '操作',
              ]}
              minWidth={1080}
            >
              {items.map(p => {
                const vendor = providerTemplates.find(t => t.id !== 'custom' && (t.id === p.provider_type || t.aliases?.includes(p.provider_type)))
                const isSelected = selected.has(p.id)
                const isOnline = p.status === 1
                const hasCred = p.credential?.configured

                return (
                  <tr key={p.id} className={isSelected ? 'selectedRow' : ''}>
                    <td className="selectionCell">
                      <input
                        type="checkbox"
                        aria-label={`选择渠道 ${p.name}`}
                        checked={isSelected}
                        onChange={() => toggleSelected(p.id)}
                      />
                    </td>
                    <td>
                      <div className="channelNameCell">
                        <b className="channelMainName mono">{p.name}</b>
                        {p.description && <p className="channelDescText">{p.description}</p>}
                      </div>
                    </td>
                    <td>
                      <span className="providerTypeBadge">
                        {vendor?.label || p.provider_type || '未分类'}
                      </span>
                    </td>
                    <td>
                      <span className="protocolBadge">{protocolLabel(p.protocol)}</span>
                      <div className="mono cut baseUrlText" title={p.base_url}>
                        {p.base_url || '—'}
                      </div>
                    </td>
                    <td>
                      <div className="channelStatusStack">
                        <span className={`statusChip ${isOnline ? 'ok' : 'down'}`}>
                          <i className={`dot ${isOnline ? 'on' : 'down'}`} aria-hidden />
                          {isOnline ? '已启用' : '已停用'}
                        </span>
                        <CircuitChip
                          circuit={circuits.get(p.name)}
                          unavailable={data?.circuits === null}
                          sampledAt={data?.sampledAt}
                        />
                      </div>
                    </td>
                    <td>
                      {hasCred ? (
                        <span className="statusChip ok">
                          <i className="dot on" aria-hidden />
                          已配置
                        </span>
                      ) : p.credential?.source_type === 'env' ? (
                        <span className="statusChip warn">
                          <i className="dot unknown" aria-hidden />
                          环境变量未就绪
                        </span>
                      ) : (
                        <span className="statusChip down">
                          <i className="dot down" aria-hidden />
                          未配置
                        </span>
                      )}
                    </td>
                    <td>
                      {p.last_test ? (
                        <div className="lastTestCell" title={when(p.last_test.at)}>
                          <div className="lastTestTime">{ago(p.last_test.at)}</div>
                          <div className={`lastTestLatency ${p.last_test.ok ? 'success' : 'danger'}`}>
                            {p.last_test.ok ? '成功' : '失败'} · {ms(p.last_test.latency_ms)}
                          </div>
                        </div>
                      ) : (
                        <span className="muted">尚未诊断</span>
                      )}
                    </td>
                    <td>
                      <div className="channelRefs">
                        <span className="candidateCountBadge" title="候选上游模型数">
                          {p.candidate_model_count ?? '—'}
                        </span>
                        <span className="channelRefCounts">
                          {p.referenced_route_count != null && <span>线路 {p.referenced_route_count}</span>}
                          {p.referenced_model_count != null && <span>对外模型 {p.referenced_model_count}</span>}
                        </span>
                      </div>
                    </td>
                    <td>
                      <div className="rowActions">
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => openDrawer(p, 'details')}
                        >
                          详情
                        </button>
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => openDrawer(p, 'test')}
                        >
                          诊断
                        </button>
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => openDrawer(p, 'basic')}
                        >
                          编辑
                        </button>
                        <button
                          type="button"
                          className="tableActionLink danger"
                          disabled={!!deleting}
                          onClick={() => void remove(p)}
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </Table>

            <Pagination page={Math.floor(offset / PAGE_SIZE) + 1} pageSize={PAGE_SIZE} total={total} loading={loading}
              onPageChange={page => {setOffset((page - 1) * PAGE_SIZE); setSelected(new Set())}} />
          </>
        ) : (
          <EmptyState
            icon={<Network />}
            title="未找到匹配的渠道"
            description="尝试调整筛选条件，或者点击上方按钮新增第一个上游渠道。"
            action={
              <Button
                variant="primary"
                icon={<Plus />}
                onClick={() => {
                  setSaveError('')
                  setTab('basic')
                  setDraft({status: 1, protocol: 'openai-chat'})
                }}
              >
                新增渠道
              </Button>
            }
          />
        )}
      </Panel>

      {draft && (
        <Drawer
          title={draft.id ? `${tab === 'basic' ? '编辑渠道' : '渠道详情'}：${activeProvider?.name || draft.name}` : '新增渠道'}
          subtitle="凭据加密保存，只写不读；也可以改为引用环境变量。"
          size="lg"
          onClose={() => {if (!saving) setDraft(null)}}
        >
          {draft.id ? (
            <div className="drawerStack">
              {activeProvider && (
                <ChannelFacts
                  provider={activeProvider}
                  circuit={circuits.get(activeProvider.name)}
                  unavailable={data?.circuits === null}
                  sampledAt={data?.sampledAt}
                />
              )}
              {/* Tabs for editing existing channel */}
              <div className="segmented" role="group" aria-label="渠道配置视图">
                <button type="button" className={`segmentedItem ${tab === 'details' ? 'on' : ''}`} onClick={() => setTab('details')}>详情</button>
                <button
                  type="button"
                  className={`segmentedItem ${tab === 'basic' ? 'on' : ''}`}
                  onClick={() => setTab('basic')}
                >
                  基本配置
                </button>
                <button
                  type="button"
                  className={`segmentedItem ${tab === 'models' ? 'on' : ''}`}
                  onClick={() => setTab('models')}
                >
                  候选模型
                </button>
                <button
                  type="button"
                  className={`segmentedItem ${tab === 'test' ? 'on' : ''}`}
                  onClick={() => setTab('test')}
                >
                  连通诊断
                </button>
                <button
                  type="button"
                  className={`segmentedItem ${tab === 'usage' ? 'on' : ''}`}
                  onClick={() => setTab('usage')}
                >
                  运行时调用
                </button>
              </div>

              {tab === 'details' && activeProvider && <>
                <Detail title="连接配置" rows={[
                  ['渠道名称', activeProvider.name], ['供应商分类', activeProvider.provider_type || '未分类'],
                  ['接口协议', protocolLabel(activeProvider.protocol)], ['Base URL', activeProvider.base_url],
                  ['说明', activeProvider.description || '未填写'],
                ]} />
                <p className="hint">停用会保留渠道和关联配置。删除会移除线路，并可能级联删除失去全部线路的对外模型。</p>
                <div className="quickActions">
                  <Button onClick={() => setTab('basic')}>编辑配置</Button>
                  <Button loading={changingStatus} onClick={async () => {
                    const p = activeProvider
                    const status = p.status === 1 ? 2 : 1
                    if (status === 2 && !await confirm({title: `停用渠道 ${p.name}？`, description: `停用后不再通过该渠道发起新的模型请求，配置和线路会保留。当前引用线路 ${p.referenced_route_count ?? '未知'} 条，关联对外模型 ${p.referenced_model_count ?? '未知'} 个；这些数量不等于将不可用的模型数量。`, confirmText: '确认停用', danger: true})) return
                    setChangingStatus(true)
                    try {
                      const out = await api<Provider>(`/admin/providers/${encodeURIComponent(p.id)}`, {method: 'PATCH', body: JSON.stringify({status})})
                      setActiveProvider(out)
                      setDraft(previous => previous ? {...previous, status: out.status} : previous)
                      toast.success(out.status === 1 ? '渠道已启用' : '渠道已停用')
                      reload()
                    } catch (e) {toast.error(e instanceof Error ? e.message : '更新渠道状态失败')}
                    finally {setChangingStatus(false)}
                  }}>{activeProvider.status === 1 ? '停用渠道' : '启用渠道'}</Button>
                  <Button variant="danger" loading={deleting === activeProvider.id} onClick={() => void remove(activeProvider)}>删除渠道</Button>
                </div>
              </>}

              {tab === 'basic' && (
                <ProviderForm
                  value={draft}
                  saving={saving}
                  error={saveError}
                  onSave={save}
                  onCancel={() => setDraft(null)}
                />
              )}

              {tab === 'models' && (
                <ProviderModelsPanel
                  providerID={draft.id}
                  onChanged={reload}
                />
              )}

              {tab === 'test' && activeProvider && (
                <ProviderTestPanel
                  provider={activeProvider}
                  canPreviewContent={canPreviewContent}
                  onTested={reload}
                />
              )}

              {tab === 'usage' && (
                <RuntimeSummaryPanel
                  kind="provider"
                  id={draft.id}
                  canOpenRawLogs={canOpenRawLogs}
                  onOpenLogs={filters => {
                    setDraft(null)
                    onOpenLogs?.(filters)
                  }}
                />
              )}
            </div>
          ) : (
            <ProviderForm
              value={draft}
              saving={saving}
              error={saveError}
              onSave={save}
              onCancel={() => setDraft(null)}
            />
          )}
        </Drawer>
      )}
    </div>
  )
}
