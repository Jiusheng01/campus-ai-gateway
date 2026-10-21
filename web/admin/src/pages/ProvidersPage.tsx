import {useCallback, useMemo, useState} from 'react'
import {
  Activity,
  AlertCircle,
  AlertTriangle,
  ArrowUpRight,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Clock,
  Eye,
  FlaskConical,
  Key,
  Layers,
  Network,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Trash2,
  XCircle,
} from 'lucide-react'
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
import {Badge, StatusBadge} from '../components/ui/Badge'
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
import {friendlyProviderName, PROTOCOLS, protocolLabel} from '../components/providers/templates'
import {PageBanner} from '../components/ui/PageBanner'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {MiniSparkline} from '../components/ui/CampusIllustrations'
import {useResource} from '../lib/useResource'
import {ago, ms, when} from '../lib/format'

const PAGE_SIZE = 15

type Filters = {name: string; provider_type: string; protocol: string; status: string}
const emptyFilters: Filters = {name: '', provider_type: '', protocol: '', status: ''}

type Tab = 'basic' | 'models' | 'test' | 'usage'

type Bundle = {
  providers: Provider[]
  total: number
  circuits: CircuitView[]
  sampledAt: Date
}

async function deleteImpact(providerID: string): Promise<ProviderImpact> {
  return api<ProviderImpact>(`/admin/providers/${providerID}/impact`)
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
      circuits: status?.circuits || [],
      sampledAt: new Date(),
    }
  }, [query])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [query])

  const save = async (v: ProviderDraft) => {
    try {
      await api(v.id ? `/admin/providers/${v.id}` : '/admin/providers', {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success(v.id ? '渠道配置已更新' : '渠道已创建')
      setDraft(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存渠道失败')
    }
  }

  const remove = async (p: Provider) => {
    let impact: ProviderImpact
    try {
      impact = await deleteImpact(p.id)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法读取该渠道的删除影响')
      return
    }
    const ok = await confirm({
      title: `删除渠道「${p.name}」？`,
      description: `将永久删除 1 个渠道、${impact.routes} 条线路、${impact.orphaned_models} 个对外模型（共影响 ${impact.affected_models.length} 个模型）。提交时会重新校验，实际结果以返回数量为准。此操作不可撤销。`,
      confirmText: '永久删除',
      danger: true,
      requireText: p.name,
      requireTextLabel: `输入渠道名「${p.name}」以确认`,
    })
    if (!ok) return
    setDeleting(p.id)
    try {
      const result = await api<ProviderDeleteResult>(`/admin/providers/${encodeURIComponent(p.id)}`, {method: 'DELETE'})
      toast.success(
        `已删除渠道：${result.deleted_routes} 条线路、${result.deleted_virtual_models} 个对外模型、${result.deleted_provider_models} 条模型清单`,
      )
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除渠道失败')
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
  const onlineCount = items.filter(p => p.status === 1).length
  const offlineCount = items.filter(p => p.status !== 1).length
  const onlinePct = total > 0 ? Math.round((onlineCount / total) * 100) : 0
  const offlinePct = total > 0 ? Math.round((offlineCount / total) * 100) : 0

  const [activeProvider, setActiveProvider] = useState<Provider | null>(null)

  const openDrawer = (p: Provider, defaultTab: Tab = 'basic') => {
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
      {/* Top Banner with Campus Illustration */}
      <PageBanner
        title="连接全球优质模型服务"
        subtitle="统一管理多种模型供应商的接入配置，支持快速切换与健康监控。"
      />

      {/* Top Summary Statistics Cards matching image 2 */}
      <section aria-label="渠道概览统计" className="providersSummarySection">
        <div className="providersSummaryHeader">
          <div className="providersSummaryStats">
            <StatCard
              label="渠道总数"
              value={total}
              icon={<Network />}
              tone="default"
              className="compactStat"
            />
            <StatCard
              label="在线"
              value={
                <span className="statWithIndicator">
                  <i className="dot on" aria-hidden />
                  {onlineCount} <small className="statSubPct">({onlinePct}%)</small>
                </span>
              }
              icon={<CheckCircle2 />}
              sparkline={<MiniSparkline data={[20, 40, 60, 80, 70, 90]} color="#10b981" />}
              tone="success"
              className="compactStat"
            />
            <StatCard
              label="异常 / 停用"
              value={
                <span className="statWithIndicator">
                  <i className={`dot ${offlineCount > 0 ? 'down' : 'unknown'}`} aria-hidden />
                  {offlineCount} <small className="statSubPct">({offlinePct}%)</small>
                </span>
              }
              icon={<AlertCircle />}
              sparkline={<MiniSparkline data={[10, 15, 12, 20, 18, 25]} color="#f59e0b" />}
              tone={offlineCount > 0 ? 'warning' : 'default'}
              className="compactStat"
            />
          </div>

          <div className="providersSummaryAction">
            <Button
              variant="primary"
              icon={<Plus />}
              onClick={() => {
                setTab('basic')
                setDraft({status: 1, protocol: 'openai-chat'})
              }}
            >
              新建渠道
            </Button>
          </div>
        </div>
      </section>

      {/* Filter Section matching image 2 */}
      <div className="filterCard">
        <div className="filtersBar">
          <label className="filterFieldInline">
            <span className="filterLabel">名称</span>
            <input
              className="filterInput"
              placeholder="请输入渠道名称"
              value={draftFilters.name}
              onChange={e => setDraftFilters({...draftFilters, name: e.target.value})}
              onKeyDown={e => e.key === 'Enter' && setFilters(draftFilters)}
            />
          </label>

          <label className="filterFieldInline">
            <span className="filterLabel">供应商类型</span>
            <select
              className="filterSelect"
              value={draftFilters.provider_type}
              onChange={e => setDraftFilters({...draftFilters, provider_type: e.target.value})}
            >
              <option value="">全部</option>
              <option value="qwen">Qwen / 通义千问</option>
              <option value="deepseek">DeepSeek</option>
              <option value="doubao">豆包 / 火山引擎</option>
              <option value="openai">OpenAI</option>
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
              <option value="1">启用 / 在线</option>
              <option value="2">停用</option>
            </select>
          </label>

          <div className="filterActionButtons">
            <Button
              onClick={() => {
                setFilters(emptyFilters)
                setDraftFilters(emptyFilters)
                setOffset(0)
              }}
            >
              重置
            </Button>
            <Button
              variant="primary"
              onClick={() => {
                setFilters(draftFilters)
                setOffset(0)
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

      {/* Main Channels Table matching image 2 */}
      <Panel title="渠道列表" sub="管理当前网关接入的模型通道、鉴权信息及测试状态">
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
                '供应商类型',
                '接口协议',
                'Base URL',
                '状态',
                '凭据状态',
                '最后测试时间',
                '候选模型',
                '操作',
              ]}
              minWidth={1180}
            >
              {items.map(p => {
                const friendly = friendlyProviderName(p.name)
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
                        {friendly && <span className="channelFriendlyName">({friendly})</span>}
                        {p.description && <p className="channelDescText">{p.description}</p>}
                      </div>
                    </td>
                    <td>
                      <span className="providerTypeBadge">
                        {p.provider_type || '自定义'}
                      </span>
                    </td>
                    <td>
                      <span className="protocolBadge">{protocolLabel(p.protocol)}</span>
                    </td>
                    <td>
                      <span className="mono cut baseUrlText" title={p.base_url}>
                        {p.base_url || '—'}
                      </span>
                    </td>
                    <td>
                      <span className={`statusChip ${isOnline ? 'ok' : 'down'}`}>
                        <i className={`dot ${isOnline ? 'on' : 'down'}`} aria-hidden />
                        {isOnline ? '在线' : '已停用'}
                      </span>
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
                        <div className="lastTestCell">
                          <div className="lastTestTime">{ago(p.last_test.at)}</div>
                          <div className={`lastTestLatency ${p.last_test.ok ? 'success' : 'danger'}`}>
                            {p.last_test.ok ? '✓' : '✗'} {ms(p.last_test.latency_ms)}
                          </div>
                        </div>
                      ) : (
                        <span className="muted">—</span>
                      )}
                    </td>
                    <td>
                      <span className="candidateCountBadge">
                        {p.candidate_model_count ?? 0}
                      </span>
                    </td>
                    <td>
                      <div className="rowActions">
                        <button
                          type="button"
                          className="tableActionLink"
                          onClick={() => openDrawer(p, 'basic')}
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
                          disabled={deleting === p.id}
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

            {/* Table Pagination Footer */}
            <footer className="tablePaginationFooter">
              <span className="paginationInfo">
                第 {offset + 1}-{Math.min(offset + PAGE_SIZE, total)} 条，共 {total} 条
              </span>
              <div className="paginationControls">
                <Button
                  size="sm"
                  icon={<ChevronLeft />}
                  disabled={offset === 0}
                  onClick={() => setOffset(prev => Math.max(0, prev - PAGE_SIZE))}
                >
                  上一页
                </Button>
                <span className="paginationCurrentPage">
                  {Math.floor(offset / PAGE_SIZE) + 1}
                </span>
                <Button
                  size="sm"
                  icon={<ChevronRight />}
                  disabled={offset + PAGE_SIZE >= total}
                  onClick={() => setOffset(prev => prev + PAGE_SIZE)}
                >
                  下一页
                </Button>
              </div>
            </footer>
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

      {/* Add / Edit Channel Drawer matching image 2 */}
      {draft && (
        <Drawer
          title={draft.id ? `编辑渠道：${draft.name}` : '新增渠道'}
          subtitle="密钥加密保存，只写不读；也可以改为引用环境变量。"
          size="lg"
          onClose={() => setDraft(null)}
        >
          {draft.id ? (
            <div className="drawerStack">
              {/* Tabs for editing existing channel */}
              <div className="segmented" role="group" aria-label="渠道配置视图">
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

              {tab === 'basic' && (
                <ProviderForm
                  value={draft}
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
              onSave={save}
              onCancel={() => setDraft(null)}
            />
          )}
        </Drawer>
      )}
    </div>
  )
}
