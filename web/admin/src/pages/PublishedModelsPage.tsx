import {useCallback, useMemo, useState} from 'react'
import {
  Boxes,
  Eye,
  FlaskConical,
  KeyRound,
  Play,
  Plus,
  Power,
  RefreshCw,
  Route,
  Search,
} from 'lucide-react'
import {
  api,
  listModelTestOptions,
  type ModelConfig,
  type ModelDetail,
  type ModelImpact,
  type ModelTestOption,
  type Provider,
  type PublishResult,
  type RouteView,
  type Tenant,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Detail} from '../components/ui/Detail'
import {Modal} from '../components/ui/Modal'
import {RouteEditor} from '../components/models/RouteEditor'
import {PublishModelWizard} from '../components/models/PublishModelWizard'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {ModelAuthorizationPanel} from '../components/models/ModelAuthorizationPanel'
import {RuntimeSummaryPanel, type LogSeed} from '../components/models/RuntimeSummaryPanel'
import {PageBanner} from '../components/ui/PageBanner'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useResource} from '../lib/useResource'
import {Pagination} from '../components/ui/Pagination'
import {CopyButton} from '../components/ui/CopyButton'
import {Table} from '../components/ui/Table'

/**
 * The channel filter needs the model → channel association, which the model
 * list does not carry: a Virtual Model reaches a channel through its Routes.
 * The route read caps one page at 500 rows, so walk pages until a short one
 * instead of filtering against a silently truncated list.
 */
async function listAllModelRoutes(): Promise<RouteView[]> {
  const pageSize = 500
  const all: RouteView[] = []
  for (let offset = 0; ; offset += pageSize) {
    const d = await api<{data: RouteView[]}>(`/admin/routes?limit=${pageSize}&offset=${offset}`)
    const page = d.data || []
    all.push(...page)
    if (page.length < pageSize) return all
  }
}

type ModelTag = {label: string; warn?: boolean}

/**
 * Tags come only from fields the gateway maintains. The alias is a client-facing
 * name, not a capability declaration — `campus-vision` proves nothing about
 * whether the model accepts images — so it is never inspected here.
 *
 * Absent booleans stay absent: an unknown state is not a false one, and the
 * card shows no tag rather than a claim it cannot support. When neither field
 * is present there is no tag row at all.
 */
function getModelTags(m: ModelConfig): ModelTag[] {
  const tags: ModelTag[] = []
  if (typeof m.statically_routable === 'boolean') {
    tags.push(m.statically_routable ? {label: '静态可路由'} : {label: '暂无可路由线路', warn: true})
  }
  if (typeof m.credential_configured === 'boolean') {
    tags.push(m.credential_configured ? {label: '凭据已配置'} : {label: '凭据未配置', warn: true})
  }
  return tags
}

export function PublishedModelsPage({
  canAuthorize = false,
  canOpenRawLogs = false,
  onOpenLogs,
}: {
  canAuthorize?: boolean
  canOpenRawLogs?: boolean
  onOpenLogs?: (filters: LogSeed) => void
}) {
  const loader = useCallback(async () => {
    // /admin/tenants is granted to super_admin only. `canAuthorize` is that
    // role's signal on this page (App passes roles.includes('super_admin')),
    // so the read is not attempted for a role the gateway will refuse.
    const tenantsPromise: Promise<Tenant[] | null> = canAuthorize
      ? api<{data: Tenant[]}>('/admin/tenants')
          .then(r => r.data || [])
          .catch(() => null)
      : Promise.resolve(null)
    const [modelsRes, providersRes, tenants, routes] = await Promise.all([
      api<{data: ModelConfig[]}>('/admin/models'),
      api<{data: Provider[]}>('/admin/providers').catch(() => null),
      tenantsPromise,
      // A failed route read leaves the channel filter unavailable rather than
      // matching every channel to nothing.
      listAllModelRoutes().catch(() => null),
    ])
    return {
      models: modelsRes.data || [],
      providers: providersRes?.data || [],
      providersAvailable: providersRes !== null,
      tenants,
      routes,
    }
  }, [canAuthorize])

  const {data, loading, error, errorStatus, reload} = useResource(loader, [canAuthorize])
  const [detail, setDetail] = useState<ModelDetail | null>(null)
  const toast = useToast()
  const confirm = useConfirm()
  const [detailBusy, setDetailBusy] = useState('')
  const [editingRoutesModel, setEditingRoutesModel] = useState<{id: string; alias: string} | null>(null)
  const [disableBusy, setDisableBusy] = useState(false)
  const [publishing, setPublishing] = useState(false)
  const [publication, setPublication] = useState<PublishResult | null>(null)
  // Disabling is irreversible for callers until the model is enabled again, so
  // the card opens the impact preview instead of a bare confirmation.
  const [impact, setImpact] = useState<{model: ModelConfig; preview: ModelImpact} | null>(null)
  const [impactError, setImpactError] = useState('')
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)
  const [testingAlias, setTestingAlias] = useState('')
  const [testBusy, setTestBusy] = useState(false)

  // Filters state
  const [searchQuery, setSearchQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [channelFilter, setChannelFilter] = useState('')
  const [page, setPage] = useState(1)
  const pageSize = 12

  const setModelStatus = async (model: ModelConfig, status: 1 | 2) => {
    setDisableBusy(true)
    try {
      await api(`/admin/models/${model.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          alias: model.alias,
          display_name: model.display_name,
          description: model.description,
          status,
          expected_version: model.config_version,
        }),
      })
      toast.success(status === 2 ? '模型已停用；已发布 alias 不会自动从 Key 白名单移除' : '模型已启用')
      setImpact(null)
      if (detail && detail.model.id === model.id) {
        await open(model)
      }
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : (status === 2 ? '停用模型失败' : '启用模型失败'))
    } finally {
      setDisableBusy(false)
    }
  }

  const openTest = async (alias = detail?.model.alias || '') => {
    setTestBusy(true)
    try {
      setTesting(await listModelTestOptions())
      setTestingAlias(alias)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型测试选项')
    } finally {
      setTestBusy(false)
    }
  }

  /**
   * Read the impact preview before disabling. The server recomputes it on the
   * PUT, so this is a preview, not a reservation; showing it first is what
   * lets the operator see the affected Routes and Keys before the outage.
   */
  const loadImpact = async (m: ModelConfig) => {
    setImpactError('')
    try {
      setImpact({model: m, preview: await api<ModelImpact>(`/admin/models/${m.id}/impact`)})
    } catch (e) {
      setImpactError(e instanceof Error ? e.message : '无法读取停用影响')
    }
  }

  const toggleModelStatus = async (m: ModelConfig) => {
    if (m.status === 1) {
      await loadImpact(m)
    } else {
      const ok = await confirm({
        title: `启用模型「${m.alias}」？`,
        description: '启用后该 alias 会在至少一条启用线路时重新进入静态可路由集合；Key 授权不变。',
        confirmText: '启用模型',
      })
      if (ok) await setModelStatus(m, 1)
    }
  }

  const open = async (m: ModelConfig, options: {authorize?: boolean} = {}) => {
    setDetailBusy(m.id)
    setDetail(null)
    try {
      const d = await api<ModelDetail>(`/admin/models/${m.id}`)
      setDetail(d)
      if (options.authorize) {
        window.setTimeout(() => {
          document.getElementById('model-authorization')?.scrollIntoView({behavior: 'smooth', block: 'start'})
        }, 100)
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型详情')
    } finally {
      setDetailBusy('')
    }
  }

  const allModels = data?.models || []
  const providers = data?.providers || []
  const tenants = data?.tenants ?? null
  const routes = data?.routes ?? null
  const totalModels = allModels.length
  const enabledModels = allModels.filter(m => m.status === 1).length
  // Null means the collection was not readable or did not load; no card then.
  const enabledTenants = tenants ? tenants.filter(t => t.status === 1).length : null

  // model id → channel names the model is routed through. Selecting a channel
  // must narrow the rendered cards, and the model list carries only counts, so
  // the association comes from the route projection.
  const channelsByModel = useMemo(() => {
    const map = new Map<string, Set<string>>()
    for (const r of routes || []) {
      const ids = map.get(r.model_id) || new Set<string>()
      ids.add(r.provider_id)
      map.set(r.model_id, ids)
    }
    return map
  }, [routes])

  // Filtered models
  const filteredModels = useMemo(() => {
    return allModels.filter(m => {
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase()
        const matchAlias = m.alias.toLowerCase().includes(q)
        const matchName = (m.display_name || '').toLowerCase().includes(q)
        const matchDesc = (m.description || '').toLowerCase().includes(q)
        if (!matchAlias && !matchName && !matchDesc) return false
      }
      if (statusFilter && String(m.status) !== statusFilter) return false
      if (routes !== null && channelFilter && !channelsByModel.get(m.id)?.has(channelFilter)) return false
      return true
    })
  }, [allModels, searchQuery, statusFilter, channelFilter, channelsByModel, routes])
  const currentPage = Math.min(page, Math.max(1, Math.ceil(filteredModels.length / pageSize)))

  return (
    <div className="stack">
      {/* Top Banner with Campus Illustration */}
      <PageBanner
        title="对外模型"
        subtitle="管理业务系统调用的模型名称与线路配置"
        actions={<Button variant="primary" icon={<Plus />} onClick={() => setPublishing(true)}>发布模型</Button>}
      />
      {publication && <div className="notice success" role="status">
        <span>{publication.created ? '已发布' : publication.route_created ? '已追加线路' : '已有相同线路'}：{publication.model.alias}。API Key 授权请单独管理。</span>
        <Button size="sm" onClick={() => void open(publication.model)}>查看发布结果</Button>
      </div>}
      {data && !error && (routes === null || !data.providersAvailable) && <div className="notice warning" role="alert">
        {data.providersAvailable ? '线路关系读取失败' : '渠道列表读取失败'}，暂无法按渠道筛选。模型基础信息仍可查看。
        <Button size="sm" loading={loading} onClick={() => void reload()}>重试加载关系</Button>
      </div>}

      {data && !error && <div aria-label="模型概览统计" className="modelListSummary">
        <span>已发布模型 <b>{totalModels}</b></span>
        <span>启用中 <b>{enabledModels}</b></span>
        {enabledTenants !== null && <span>启用租户 <b>{enabledTenants}</b></span>}
      </div>}

      {/* Filters narrow the rendered cards only; the summary above keeps
          counting the whole model list. */}
      <div className="filterCard">
        <div className="filtersBar">
          <div className="searchFieldGroup">
            <Search className="searchFieldIcon" aria-hidden />
            <input
              className="filterSearchInput"
              placeholder="搜索对外名称、显示名称或描述..."
              value={searchQuery}
              onChange={e => { setSearchQuery(e.target.value); setPage(1) }}
            />
          </div>

          <label className="filterFieldInline">
            <span className="filterLabel">状态</span>
            <select
              className="filterSelect"
              value={statusFilter}
              onChange={e => { setStatusFilter(e.target.value); setPage(1) }}
            >
              <option value="">全部状态</option>
              <option value="1">启用中</option>
              <option value="2">已停用</option>
            </select>
          </label>

          {/* Without the route projection there is no model → channel mapping,
              so the control is not shown rather than shown doing nothing. */}
          {routes && data?.providersAvailable && (
            <label className="filterFieldInline">
              <span className="filterLabel">上游渠道</span>
              <select
                className="filterSelect"
                value={channelFilter}
                onChange={e => { setChannelFilter(e.target.value); setPage(1) }}
              >
                <option value="">全部上游渠道</option>
                {providers.map(p => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
          )}

          <div className="filterActionButtons">
            <Button
              icon={<RefreshCw />}
              onClick={() => {
                setSearchQuery('')
                setStatusFilter('')
                setChannelFilter('')
                setPage(1)
              }}
            >
              重置
            </Button>
          </div>
        </div>
      </div>

      <Panel
        title="对外模型列表"
        sub="业务系统调用 /v1/* 时使用这里的名称（alias）。渠道与线路在路由策略中维护。"
      >
        {loading && !data ? (
          <SkeletonCards count={3} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : filteredModels.length ? (
          <Table heads={['对外模型 / Alias', '渠道 → 上游模型', '线路配置', '状态', '操作']} minWidth={1080}>
            {filteredModels.slice((currentPage - 1) * pageSize, currentPage * pageSize).map(m => {
              const isOnline = m.status === 1
              const tags = getModelTags(m)
              const modelRoutes = (routes || []).filter(r => r.model_id === m.id)

              return (
                <tr key={m.id}>
                  <td><div className="modelListIdentity">
                    <strong className="mono">{m.alias}</strong>
                    {m.display_name && <span>{m.display_name}</span>}
                    <small className="muted" title={m.description}>{m.description || '—'}</small>
                    <CopyButton value={m.alias} label="复制模型 ID" />
                  </div></td>
                  <td><div className="modelListRelations">
                    {routes === null ? <span className="muted">关系读取失败</span> : modelRoutes.length ? <>
                      <small className="muted">{new Set(modelRoutes.map(r => r.provider_id)).size} 个渠道</small>
                      {modelRoutes.slice(0, 2).map(r => <div key={r.id}>
                        <span>{r.provider_name}</span>
                        <span className="mono">{r.upstream_model}</span>
                      </div>)}
                      {modelRoutes.length > 2 && <Button size="sm" onClick={() => void open(m)}>查看全部 {modelRoutes.length} 条线路</Button>}
                    </> : <span className="muted">暂无线路</span>}
                  </div></td>
                  <td><div className="modelListConfig">
                    <span>{m.enabled_route_count ?? '—'} / {m.route_count ?? '—'} 条启用</span>
                    <span>{m.routing_mode_summary || '未配置'}</span>
                    <small className="muted">配置版本 v{m.config_version}</small>
                    {tags.map(t => <small key={t.label} className={t.warn ? 'modelListWarning' : 'muted'}>{t.label}</small>)}
                  </div></td>
                  <td><StatusBadge status={m.status} /></td>
                  <td><div className="modelListActions">
                      <Button
                        size="sm"
                        icon={<Eye />}
                        loading={detailBusy === m.id}
                        onClick={() => void open(m)}
                      >
                        详情
                      </Button>
                      {canAuthorize && (
                        <Button
                          size="sm"
                          icon={<KeyRound />}
                          loading={detailBusy === m.id}
                          onClick={() => void open(m, {authorize: true})}
                        >
                          管理授权
                        </Button>
                      )}
                      <Button
                        size="sm"
                        icon={<Route />}
                        onClick={() => setEditingRoutesModel({id: m.id, alias: m.alias})}
                      >
                        路由策略
                      </Button>
                      <Button size="sm" icon={<FlaskConical />} loading={testBusy} disabled={!isOnline} title={!isOnline ? '启用模型后可测试' : '管理诊断不经过 API Key 与租户配额'} onClick={() => void openTest(m.alias)}>模型测试</Button>
                      <Button
                        size="sm"
                        variant={isOnline ? 'danger' : 'primary'}
                        icon={isOnline ? <Power /> : <Play />}
                        disabled={disableBusy}
                        onClick={() => void toggleModelStatus(m)}
                      >
                        {isOnline ? '停用' : '启用'}
                      </Button>
                  </div></td>
                </tr>
              )
            })}
          </Table>
        ) : allModels.length ? (
          // The list is not empty; the filters hid every card. Saying "暂无
          // 对外模型" here would claim the deployment has none.
          <EmptyState
            icon={<Search />}
            title="没有符合筛选条件的模型"
            description="调整搜索、状态或上游渠道后重试。"
          />
        ) : (
          <EmptyState
            icon={<Boxes />}
            title="暂无对外模型"
            description="从已启用渠道的候选模型发布第一个对外名称。"
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setPublishing(true)}>
                发布模型
              </Button>
            }
          />
        )}
      </Panel>
      {!error && data && <Pagination page={currentPage} pageSize={pageSize} total={filteredModels.length} loading={loading} onPageChange={setPage} />}

      {/* Publish Model Wizard Drawer */}
      {publishing && (
        <PublishModelWizard
          onClose={() => setPublishing(false)}
          onPublished={result => {
            setPublication(result)
            void reload()
          }}
        />
      )}

      {/* Route Editor Modal for quick route access */}
      {editingRoutesModel && (
        <Modal
          title={`线路策略：${editingRoutesModel.alias}`}
          subtitle="整组保存，带配置版本校验与实时 diff 检查。"
          onClose={() => setEditingRoutesModel(null)}
        >
          <RouteEditor
            modelID={editingRoutesModel.id}
            onSaved={() => {
              void reload()
            }}
          />
        </Modal>
      )}

      {/* Model Detail Drawer */}
      {detail && (
        <Drawer
          title={`模型详情：${detail.model.alias}`}
          subtitle="对外模型配置、授权名单与运行时调用"
          size="lg"
          onClose={() => setDetail(null)}
        >
          <div className="drawerStack">
            <Detail
              title="基本信息"
              rows={[
                ['对外名称（alias）', <span key="alias" className="modelListIdentity"><span className="mono">{detail.model.alias}</span><CopyButton value={detail.model.alias} label="复制模型 ID" /></span>],
                ['显示名称', detail.model.display_name || '—'],
                ['描述', detail.model.description || '—'],
                ['状态', <StatusBadge key="status" status={detail.model.status} />],
                ['配置版本', <span key="ver" className="mono">{detail.model.config_version}</span>],
                ['静态可路由', typeof detail.model.statically_routable !== 'boolean' ? '未知' : detail.model.statically_routable ? <Badge key="r1" tone="success">是</Badge> : <Badge key="r2" tone="warning">否</Badge>],
                ['路由模式总结', detail.model.routing_mode_summary || '未配置'],
              ]}
            />

            <Panel
              title="当前路由线路"
              sub={`该模型配置了 ${detail.routes.length} 条线路`}
              actions={
                <Button
                  size="sm"
                  icon={<Route />}
                  onClick={() => setEditingRoutesModel({id: detail.model.id, alias: detail.model.alias})}
                >
                  编辑线路
                </Button>
              }
            >
              {detail.routes.length ? (
                <div className="compactRouteList">
                  {detail.routes.map((r, i) => (
                    <div className="compactRouteRow" key={r.id}>
                      <span className="compactRouteIndex">#{i + 1}</span>
                      <b className="compactRouteProvider">{r.provider_name}</b>
                      <span className="compactRouteModel"><span className="mono">{r.upstream_model}</span> <CopyButton value={r.upstream_model} label="复制上游模型" /></span>
                      <span className="compactRoutePriority">优先级 {r.priority}</span>
                      <span className="compactRouteWeight">权重 {r.weight}</span>
                      <StatusBadge status={r.status} />
                    </div>
                  ))}
                </div>
              ) : (
                <p className="hint">暂无配置线路，客户端调用此模型将返回 404。</p>
              )}
            </Panel>

            {/* Model Authorizations Panel */}
            <div id="model-authorization">
              <ModelAuthorizationPanel
                modelID={detail.model.id}
                canAuthorize={canAuthorize}
              />
            </div>

            {/* The diagnostic predates the redesign and must stay reachable
                from the model it concerns, not only from the dashboard. */}
            <Panel title="模型测试" sub="管理诊断不经过业务 API Key、租户配额或业务请求日志。">
              <div className="formActions">
                <Button icon={<FlaskConical />} onClick={() => void openTest()}>运行模型诊断</Button>
              </div>
              <p className="hint">
                真实业务链路验证请使用环境变量中的 Key；该请求会经过鉴权、配额、路由、熔断和日志，不由管理诊断替代。
              </p>
              <pre className="mono responseText curlSample">{`curl "$GATEWAY_BASE_URL/v1/chat/completions" \\
  -H "Authorization: Bearer $CAMPUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${detail.model.alias}","messages":[{"role":"user","content":"hello"}]}'`}</pre>
            </Panel>

            {/* Runtime Summary Telemetry Panel */}
            <RuntimeSummaryPanel
              kind="model"
              id={detail.model.id}
              canOpenRawLogs={canOpenRawLogs}
              onOpenLogs={filters => {
                setDetail(null)
                onOpenLogs?.(filters)
              }}
            />
          </div>
        </Drawer>
      )}

      {testing && (
        <ModelTestDrawer
          models={testing}
          initialModel={testingAlias}
          onClose={() => setTesting(null)}
        />
      )}

      {/* Disabling preview: the server recomputes this on the PUT, so the
          dialog states it is a preview rather than a reservation. */}
      {impact && (
        <Modal
          title={`停用模型：${impact.preview.alias}`}
          subtitle="这是影响预览；提交后服务端会重新校验并立即停用。"
          onClose={() => setImpact(null)}
          footer={
            <>
              <Button onClick={() => setImpact(null)}>取消</Button>
              <Button variant="danger" loading={disableBusy} onClick={() => void setModelStatus(impact.model, 2)}>
                确认停用
              </Button>
            </>
          }
        >
          <div className="drawerStack">
            <p>{`启用线路：${impact.preview.enabled_routes} 条；启用渠道：${impact.preview.enabled_providers} 个。`}</p>
            <p>{`显式允许该 alias 的 Key：${impact.preview.explicit_keys} 个；通配符 Key：${impact.preview.wildcard_keys} 个。`}</p>
            <p className="hint">停用不会修改任何 Key 的白名单。通配符 Key 仍允许全部模型，但该 alias 不再出现在静态可路由集合中。</p>
            <p className="hint">本实例保存后立即生效；其他实例在 5 秒 TTL 内收敛；数据库故障时可能继续使用旧快照。</p>
          </div>
        </Modal>
      )}

      {impactError && (
        <div className="notice warning" role="alert">{impactError}</div>
      )}
    </div>
  )
}
