import {useCallback, useMemo, useState} from 'react'
import {
  AlertCircle,
  Boxes,
  CheckCircle2,
  ChevronRight,
  Eye,
  FlaskConical,
  KeyRound,
  Play,
  Plus,
  Power,
  RefreshCw,
  Route,
  Search,
  Sparkles,
  Users,
} from 'lucide-react'
import {
  api,
  listModelTestOptions,
  type ModelConfig,
  type ModelDetail,
  type ModelImpact,
  type ModelRoute,
  type ModelTestOption,
  type Provider,
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
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {MiniSparkline} from '../components/ui/CampusIllustrations'
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useResource} from '../lib/useResource'

function getModelTags(m: ModelConfig): string[] {
  const alias = m.alias.toLowerCase()
  const tags: string[] = []
  if (alias.includes('embed') || alias.includes('vector')) {
    tags.push('向量化', '检索增强', '知识库')
  } else if (alias.includes('vision') || alias.includes('image') || alias.includes('vl')) {
    tags.push('多模态', '图像理解', '视觉分析')
  } else if (alias.includes('audio') || alias.includes('asr') || alias.includes('tts') || alias.includes('voice')) {
    tags.push('语音处理', '音频模型')
  } else {
    tags.push('对话生成', '通用能力')
    if (m.statically_routable) tags.push('推荐')
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
    const [modelsRes, providersRes] = await Promise.all([
      api<{data: ModelConfig[]}>('/admin/models'),
      api<{data: Provider[]}>('/admin/providers?limit=100').catch(() => ({data: []})),
    ])
    return {
      models: modelsRes.data || [],
      providers: providersRes.data || [],
    }
  }, [])

  const {data, loading, error, errorStatus, reload} = useResource(loader)
  const [detail, setDetail] = useState<ModelDetail | null>(null)
  const toast = useToast()
  const confirm = useConfirm()
  const [detailBusy, setDetailBusy] = useState('')
  const [detailError, setDetailError] = useState('')
  const [editingRoutesModel, setEditingRoutesModel] = useState<{id: string; alias: string} | null>(null)
  const [impact, setImpact] = useState<ModelImpact | null>(null)
  const [impactError, setImpactError] = useState('')
  const [disableBusy, setDisableBusy] = useState(false)
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)
  const [publishing, setPublishing] = useState(false)
  const [focusAuthorization, setFocusAuthorization] = useState(false)

  // Filters state
  const [searchQuery, setSearchQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [channelFilter, setChannelFilter] = useState('')

  const openTest = async () => {
    try {
      setTesting(await listModelTestOptions())
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '无法加载模型测试选项')
    }
  }

  const loadImpact = async (m: ModelConfig) => {
    setImpactError('')
    try {
      setImpact(await api<ModelImpact>(`/admin/models/${m.id}/impact`))
    } catch (e) {
      setImpactError(e instanceof Error ? e.message : '无法读取停用影响')
    }
  }

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

  const toggleModelStatus = async (m: ModelConfig) => {
    if (m.status === 1) {
      const ok = await confirm({
        title: `停用模型「${m.alias}」？`,
        description: '停用后该 alias 将立即从静态可路由集合中移除，调用将返回 404；已授权的 Key 白名单保留，重新启用后可继续调用。',
        confirmText: '确认停用',
        danger: true,
      })
      if (ok) await setModelStatus(m, 2)
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
    setDetailError('')
    setDetail(null)
    setFocusAuthorization(options.authorize === true)
    try {
      const d = await api<ModelDetail>(`/admin/models/${m.id}`)
      setDetail(d)
      if (options.authorize) {
        window.setTimeout(() => {
          document.getElementById('model-authorization')?.scrollIntoView({behavior: 'smooth', block: 'start'})
        }, 100)
      }
    } catch (e) {
      setDetailError(e instanceof Error ? e.message : '无法加载模型详情')
    } finally {
      setDetailBusy('')
    }
  }

  const allModels = data?.models || []
  const providers = data?.providers || []
  const totalModels = allModels.length
  const enabledModels = allModels.filter(m => m.status === 1).length
  const totalRoutes = allModels.reduce((acc, m) => acc + (m.route_count ?? 0), 0)

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
      return true
    })
  }, [allModels, searchQuery, statusFilter])

  return (
    <div className="stack">
      {/* Top Banner with Campus Illustration */}
      <PageBanner
        title="对外模型"
        subtitle="管理业务系统调用的模型名称与线路配置"
      />

      {/* Top Summary Statistics Cards matching image 3 */}
      <section aria-label="模型概览统计" className="providersSummarySection">
        <div className="providersSummaryHeader">
          <div className="providersSummaryStats">
            <StatCard
              label="已发布模型"
              value={totalModels}
              icon={<Boxes />}
              subtext="为业务系统提供的对外模型总数"
              tone="default"
              className="compactStat"
            />
            <StatCard
              label="启用中"
              value={enabledModels}
              icon={<Play />}
              subtext="当前处于启用状态的模型"
              sparkline={<MiniSparkline data={[4, 6, 8, 10, 9, 11, 10, 12]} color="#10b981" />}
              tone="success"
              className="compactStat"
            />
            <StatCard
              label="关联路由线路"
              value={totalRoutes}
              icon={<Route />}
              subtext="所有对外模型配置的路由线路总计"
              sparkline={<MiniSparkline data={[8, 12, 14, 18, 22, 26, 30, 36]} color="#8b5cf6" />}
              tone="purple"
              className="compactStat"
            />
          </div>

          <div className="providersSummaryAction">
            <Button
              variant="primary"
              icon={<Plus />}
              onClick={() => setPublishing(true)}
            >
              发布模型
            </Button>
          </div>
        </div>
      </section>

      {/* Filter Row matching image 3 */}
      <div className="filterCard">
        <div className="filtersBar">
          <label className="filterField flexGrow">
            <span className="filterLabel">搜索</span>
            <div className="searchFieldWrapper">
              <Search className="fieldSearchIcon" aria-hidden />
              <input
                className="filterInput withIcon"
                placeholder="搜索对外名称、显示名称或描述..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
              />
            </div>
          </label>

          <label className="filterField">
            <span className="filterLabel">状态</span>
            <select
              className="filterSelect"
              value={statusFilter}
              onChange={e => setStatusFilter(e.target.value)}
            >
              <option value="">全部状态</option>
              <option value="1">启用中</option>
              <option value="2">已停用</option>
            </select>
          </label>

          <label className="filterField">
            <span className="filterLabel">上游渠道</span>
            <select
              className="filterSelect"
              value={channelFilter}
              onChange={e => setChannelFilter(e.target.value)}
            >
              <option value="">全部上游渠道</option>
              {providers.map(p => (
                <option key={p.id} value={p.name}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>

          <div className="filterActionButtons">
            <Button
              icon={<RefreshCw />}
              onClick={() => {
                setSearchQuery('')
                setStatusFilter('')
                setChannelFilter('')
              }}
            >
              重置
            </Button>
          </div>
        </div>
      </div>

      {/* Models List / Cards matching image 3 */}
      <Panel
        title="对外模型列表"
        sub="业务系统调用 /v1/* 时使用这里的名称（alias）。渠道与线路在路由策略中维护。"
      >
        {loading && !data ? (
          <SkeletonCards count={3} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : filteredModels.length ? (
          <div className="modelCardsGrid">
            {filteredModels.map(m => {
              const isOnline = m.status === 1
              const tags = getModelTags(m)

              return (
                <article
                  className={`modelCardNew ${isOnline ? 'active' : 'disabled'}`}
                  key={m.id}
                >
                  <header className="modelCardNewHead">
                    <div className="modelCardIdentity">
                      <div className="modelCardIconBadge">
                        <Sparkles aria-hidden />
                      </div>
                      <div className="modelCardNameBlock">
                        <div className="modelCardAliasRow">
                          <h3 className="modelCardAlias mono">{m.alias}</h3>
                          <span className={`statusPill ${isOnline ? 'ok' : 'down'}`}>
                            <i className={`dot ${isOnline ? 'on' : 'down'}`} aria-hidden />
                            {isOnline ? '启用中' : '已停用'}
                          </span>
                        </div>
                        {m.display_name && (
                          <span className="modelCardDisplayName">{m.display_name}</span>
                        )}
                      </div>
                    </div>

                    <div className="modelCardMetaRight">
                      <span className="configVersionBadge">配置版本 v{m.config_version}</span>
                    </div>
                  </header>

                  <p className="modelCardDescription">
                    {m.description || '校园通用大语言模型，适用于问答、写作、分析等多种场景。'}
                  </p>

                  <div className="modelCardTags">
                    {tags.map(t => (
                      <span key={t} className="modelTagBadge">
                        {t}
                      </span>
                    ))}
                  </div>

                  <div className="modelCardMetaDivider" />

                  <div className="modelCardInfoGrid">
                    <div className="modelCardInfoItem">
                      <span className="infoItemLabel">线路状态</span>
                      <span className="infoItemValue">
                        {m.enabled_route_count ?? 0} / {m.route_count ?? 0} 条启用
                      </span>
                    </div>
                    <div className="modelCardInfoItem">
                      <span className="infoItemLabel">路由模式</span>
                      <span className="infoItemValue">
                        {m.routing_mode_summary || '智能路由'}
                      </span>
                    </div>
                    <div className="modelCardInfoItem">
                      <span className="infoItemLabel">静态可路由性</span>
                      <span className="infoItemValue">
                        {m.statically_routable ? (
                          <span className="textSuccess">✓ 正常可路由</span>
                        ) : (
                          <span className="textWarning">⚠ 暂无可路由线路</span>
                        )}
                      </span>
                    </div>
                    <div className="modelCardInfoItem">
                      <span className="infoItemLabel">凭据状态</span>
                      <span className="infoItemValue">
                        {m.credential_configured ? '来源已配置' : '未检测到可用凭据'}
                      </span>
                    </div>
                  </div>

                  <footer className="modelCardActions">
                    <div className="modelCardActionsLeft">
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
                        查看路由
                      </Button>
                    </div>

                    <div className="modelCardActionsRight">
                      <Button
                        size="sm"
                        variant={isOnline ? 'danger' : 'primary'}
                        icon={isOnline ? <Power /> : <Play />}
                        disabled={disableBusy}
                        onClick={() => void toggleModelStatus(m)}
                      >
                        {isOnline ? '停用' : '启用'}
                      </Button>
                    </div>
                  </footer>
                </article>
              )
            })}
          </div>
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

      {/* Publish Model Wizard Drawer */}
      {publishing && (
        <PublishModelWizard
          onClose={() => setPublishing(false)}
          onPublished={() => {
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
                ['对外名称（alias）', <span key="alias" className="mono">{detail.model.alias}</span>],
                ['显示名称', detail.model.display_name || '—'],
                ['描述', detail.model.description || '—'],
                ['状态', <StatusBadge key="status" status={detail.model.status} />],
                ['配置版本', <span key="ver" className="mono">{detail.model.config_version}</span>],
                ['静态可路由', detail.model.statically_routable ? <Badge key="r1" tone="success">是</Badge> : <Badge key="r2" tone="warning">否</Badge>],
                ['路由模式总结', detail.model.routing_mode_summary || '智能路由'],
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
                      <span className="mono compactRouteModel">{r.upstream_model}</span>
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
    </div>
  )
}
