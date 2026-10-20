import {useCallback, useState} from 'react'
import {Boxes, ChevronDown, FlaskConical, Pencil, Plus, RefreshCw, Route, Trash2} from 'lucide-react'
import {
  api,
  type CatalogModel,
  type CatalogOpenResult,
  type CatalogProvider,
  type ModelConfig,
  type ModelRoute,
  type Provider,
  type ProviderModel,
} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge, StatusBadge} from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonCards} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {ModelForm, type ModelDraft} from '../components/models/ModelForm'
import {RouteForm, type RouteDraft} from '../components/models/RouteForm'
import {ModelTestDrawer} from '../components/models/ModelTestDrawer'
import {friendlyProviderName} from '../components/providers/templates'
import {useResource} from '../lib/useResource'

// The Models page opens on the catalog: "I configured a channel, what can it
// serve?" is the first question after channel setup. Routes and their
// priority/weight/timeout are a second question, so the layer that answers them
// lives behind one explicit switch instead of in front of a new operator.

type View = 'catalog' | 'advanced'

export function ModelsPage() {
  const [view, setView] = useState<View>('catalog')

  return (
    <div className="stack">
      <div className="quickActions">
        <div className="segmented" role="group" aria-label="模型视图">
          <button
            type="button"
            className={`segmentedItem ${view === 'catalog' ? 'on' : ''}`}
            aria-pressed={view === 'catalog'}
            onClick={() => setView('catalog')}
          >
            模型目录
          </button>
          <button
            type="button"
            className={`segmentedItem ${view === 'advanced' ? 'on' : ''}`}
            aria-pressed={view === 'advanced'}
            onClick={() => setView('advanced')}
          >
            对外模型与线路（高级）
          </button>
        </div>
      </div>

      {view === 'catalog' ? <CatalogView /> : <AdvancedView />}
    </div>
  )
}

// --- catalog ---------------------------------------------------------------

type CatalogBundle = {models: CatalogModel[]; providers: Provider[]}

function CatalogView() {
  const toast = useToast()
  const [opening, setOpening] = useState('')
  const [refreshing, setRefreshing] = useState('')
  const [refreshTarget, setRefreshTarget] = useState('')

  const loader = useCallback(async (): Promise<CatalogBundle> => {
    const [c, p] = await Promise.all([
      api<{data: CatalogModel[]}>('/admin/model-catalog'),
      api<{data: Provider[]}>('/admin/providers'),
    ])
    return {models: c.data || [], providers: p.data || []}
  }, [])

  const {data, loading, error, errorStatus, reload} = useResource(loader)

  const models = data?.models || []
  const providers = data?.providers || []
  const target = refreshTarget || providers[0]?.id || ''

  const open = async (p: CatalogProvider) => {
    setOpening(p.provider_model_id)
    try {
      const res = await api<CatalogOpenResult>('/admin/model-catalog/open', {
        method: 'POST',
        body: JSON.stringify({provider_model_id: p.provider_model_id}),
      })
      if (res.created) {
        toast.success(`已开放 ${res.model.alias}：业务系统现在可以调用它`)
      } else if (res.route_created) {
        toast.success(`已为 ${res.model.alias} 追加一条线路（${p.name}）`)
      } else {
        toast.info(`${res.model.alias} 已通过 ${p.name} 开放，无需重复操作`)
      }
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '开放模型失败')
    } finally {
      setOpening('')
    }
  }

  const refresh = async (provider: Provider) => {
    setRefreshing(provider.id)
    try {
      const res = await api<{data: ProviderModel[]; added: number}>(
        `/admin/providers/${provider.id}/models/refresh`,
        {method: 'POST'},
      )
      toast.success(
        res.added
          ? `已从 ${provider.name} 刷新，新增 ${res.added} 个模型`
          : `${provider.name} 的模型清单已是最新`,
      )
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '从上游刷新失败')
    } finally {
      setRefreshing('')
    }
  }

  const selected = providers.find(p => p.id === target)

  return (
    <Panel
      title="模型目录"
      sub="各渠道当前可提供的上游模型。开放后业务系统即可用同名 model 调用；同名模型由多个渠道提供时，再开放另一个渠道会追加一条故障切换线路。"
      actions={
        providers.length ? (
          <div className="catalogRefresh">
            <select
              value={target}
              aria-label="选择要刷新的渠道"
              onChange={e => setRefreshTarget(e.target.value)}
            >
              {providers.map(p => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
            <Button
              icon={<RefreshCw />}
              loading={!!selected && refreshing === selected.id}
              disabled={!selected || refreshing !== ''}
              onClick={() => selected && void refresh(selected)}
            >
              从上游刷新
            </Button>
          </div>
        ) : undefined
      }
    >
      {loading && !data ? (
        <SkeletonCards count={2} />
      ) : error ? (
        <ErrorState message={error} status={errorStatus} onRetry={reload} />
      ) : models.length ? (
        <div className="catalogList">
          {models.map(m => (
            <article className="catalogRow" key={m.upstream_model}>
              <header className="catalogHead">
                <div className="modelIdentity">
                  <h3 className="modelAlias mono">{m.upstream_model}</h3>
                  <p className="modelHint">
                    业务系统使用 <code className="mono">model={m.upstream_model}</code>
                  </p>
                </div>
                {m.opened ? <Badge tone="success">已开放</Badge> : <Badge tone="warning">未开放</Badge>}
              </header>

              <div className="catalogProviders">
                {m.providers.map(p => {
                  const friendly = friendlyProviderName(p.name)
                  return (
                    <div className="catalogProvider" key={p.provider_model_id}>
                      <div className="catalogProviderName">
                        {friendly && <span className="providerFriendly">{friendly}</span>}
                        <b>{p.name}</b>
                      </div>
                      <div className="catalogProviderActions">
                        <Button
                          size="sm"
                          icon={<RefreshCw />}
                          loading={refreshing === p.id}
                          disabled={refreshing !== '' || opening !== ''}
                          onClick={() => {
                            const provider = providers.find(x => x.id === p.id)
                            if (provider) void refresh(provider)
                          }}
                        >
                          从上游刷新
                        </Button>
                        {p.opened ? (
                          <Badge tone="success">已接入</Badge>
                        ) : (
                          <Button
                            size="sm"
                            variant="primary"
                            icon={<Plus />}
                            loading={opening === p.provider_model_id}
                            disabled={opening !== '' || refreshing !== ''}
                            onClick={() => void open(p)}
                          >
                            开放
                          </Button>
                        )}
                      </div>
                    </div>
                  )
                })}
              </div>
            </article>
          ))}
        </div>
      ) : (
        <EmptyState
          icon={<Boxes />}
          title="暂无可开放的模型"
          description={
            providers.length
              ? '渠道还没有模型清单。用右上角的「从上游刷新」拉取，或在「渠道」页为它手动补充模型名。'
              : '请先在「渠道」页添加渠道，渠道的模型清单会出现在这里。'
          }
        />
      )}
    </Panel>
  )
}

// --- advanced: virtual models and routes -----------------------------------

type ModelBundle = {models: ModelConfig[]; providers: Provider[]; routes: Record<string, ModelRoute[]>}

type DrawerState =
  | {kind: 'model'; draft: ModelDraft}
  | {kind: 'route'; model: ModelConfig; draft: RouteDraft}
  | {kind: 'test'; model?: string}
  | null

function AdvancedView() {
  const toast = useToast()
  const confirm = useConfirm()
  const [drawer, setDrawer] = useState<DrawerState>(null)

  const loader = useCallback(async (): Promise<ModelBundle> => {
    const [m, p] = await Promise.all([
      api<{data: ModelConfig[]}>('/admin/models'),
      api<{data: Provider[]}>('/admin/providers'),
    ])
    const models = m.data || []
    const routePairs = await Promise.all(
      models.map(async x => {
        const r = await api<{data: ModelRoute[]}>(`/admin/models/${x.id}/routes`)
        return [x.id, r.data || []] as const
      }),
    )
    return {
      models,
      providers: p.data || [],
      routes: Object.fromEntries(routePairs),
    }
  }, [])

  const {data, loading, error, errorStatus, reload} = useResource(loader)

  const saveModel = async (v: ModelDraft) => {
    try {
      await api(v.id ? `/admin/models/${v.id}` : '/admin/models', {method: v.id ? 'PUT' : 'POST', body: JSON.stringify(v)})
      toast.success('模型已保存')
      setDrawer(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存模型失败')
    }
  }

  const saveRoute = async (model: ModelConfig, v: RouteDraft) => {
    try {
      await api(v.id ? `/admin/model-routes/${v.id}` : `/admin/models/${model.id}/routes`, {
        method: v.id ? 'PUT' : 'POST',
        body: JSON.stringify(v),
      })
      toast.success('线路已保存')
      setDrawer(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '保存线路失败')
    }
  }

  const removeRoute = async (r: ModelRoute) => {
    const ok = await confirm({
      title: '删除备用线路？',
      description: `${r.provider_name} → ${r.upstream_model}。删除后该线路不再参与故障切换。`,
      confirmText: '删除线路',
      danger: true,
    })
    if (!ok) return
    try {
      await api(`/admin/model-routes/${r.id}`, {method: 'DELETE'})
      toast.success('线路已删除')
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : '删除线路失败')
    }
  }

  const models = data?.models || []
  const providers = data?.providers || []

  return (
    <div className="stack">
      <div className="quickActions">
        <Button variant="primary" icon={<FlaskConical />} onClick={() => setDrawer({kind: 'test'})}>
          模型连通测试
        </Button>
      </div>

      <Panel
        title="对外模型与线路"
        sub="高级视图：业务系统只使用统一的对外模型名称，实际渠道和上游模型在这里配置并进行故障切换。日常开放模型请用「模型目录」。"
        actions={
          <Button
            variant="primary"
            icon={<Plus />}
            disabled={!providers.length}
            onClick={() => setDrawer({kind: 'model', draft: {status: 1}})}
          >
            新增模型
          </Button>
        }
      >
        {loading && !data ? (
          <SkeletonCards count={2} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : models.length ? (
          <div className="modelCards">
            {models.map(m => {
              const rs = (data?.routes[m.id] || []).slice().sort((a, b) => a.priority - b.priority || b.weight - a.weight)
              const primaryIdx = rs.findIndex(r => r.status === 1)
              return (
                <article className={`modelCard ${m.status === 1 ? '' : 'disabled'}`} key={m.id}>
                  <header className="modelHead">
                    <div className="modelIdentity">
                      <div className="modelAliasRow">
                        <h3 className="modelAlias mono">{m.alias}</h3>
                        <StatusBadge status={m.status} />
                      </div>
                      <span className="modelKind">对外模型</span>
                      <p className="modelHint">
                        业务系统使用 <code className="mono">model={m.alias}</code>
                      </p>
                    </div>
                    <div className="modelActions">
                      <Button size="sm" icon={<FlaskConical />} onClick={() => setDrawer({kind: 'test', model: m.alias})}>
                        模型测试
                      </Button>
                      <Button size="sm" icon={<Pencil />} onClick={() => setDrawer({kind: 'model', draft: m})}>
                        编辑模型
                      </Button>
                    </div>
                  </header>

                  <div className="routeChain">
                    {rs.map((r, i) => {
                      const isPrimary = i === primaryIdx
                      const friendly = friendlyProviderName(r.provider_name)
                      return (
                        <div className="routeChainItem" key={r.id}>
                          {i > 0 && (
                            <div className="routeConnector" aria-hidden>
                              <ChevronDown />
                              <span>失败自动切换</span>
                            </div>
                          )}
                          <div className={`routeStep ${r.status === 1 ? '' : 'isDisabled'} ${isPrimary ? 'isPrimary' : ''}`}>
                            <span className="routeIndex" aria-hidden>
                              {i + 1}
                            </span>
                            <div className="routeBox">
                              <div className="routeBoxHead">
                                <b>{friendly || r.provider_name}</b>
                                <span className="routeTags">
                                  {isPrimary ? <Badge tone="info">主线路</Badge> : <Badge>备用线路</Badge>}
                                  <StatusBadge status={r.status} />
                                </span>
                              </div>
                              {friendly && <span className="routeProvider mono">{r.provider_name}</span>}
                              <code className="routeModel mono">{r.upstream_model}</code>
                              <div className="routeMeta">
                                <span>
                                  优先级 <b>{r.priority}</b>
                                </span>
                                <span>
                                  流量权重 <b>{r.weight}</b>
                                </span>
                                <span>
                                  超时 <b>{r.timeout_ms} ms</b>
                                </span>
                              </div>
                              <div className="routeActions">
                                <button
                                  className="link"
                                  onClick={() =>
                                    setDrawer({
                                      kind: 'route',
                                      model: m,
                                      draft: r,
                                    })
                                  }
                                >
                                  编辑
                                </button>
                                <button className="link dangerText" onClick={() => void removeRoute(r)}>
                                  <Trash2 aria-hidden />
                                  删除
                                </button>
                              </div>
                            </div>
                          </div>
                        </div>
                      )
                    })}
                    {!rs.length && (
                      <EmptyState title="该模型尚未配置线路" description="添加一条线路后，业务请求才能被路由到渠道。" />
                    )}
                  </div>

                  <button
                    className="addRoute"
                    onClick={() =>
                      setDrawer({
                        kind: 'route',
                        model: m,
                        draft: {priority: rs.length + 1, weight: 100, timeout_ms: 60000, status: 1},
                      })
                    }
                  >
                    <Plus aria-hidden />
                    添加备用线路
                  </button>
                </article>
              )
            })}
          </div>
        ) : (
          <EmptyState
            icon={<Route />}
            title="暂无对外模型"
            description={
              providers.length
                ? '在「模型目录」里开放一个渠道模型，就会自动创建对外模型与第一条线路。'
                : '请先添加渠道，再创建对外模型。'
            }
            action={
              providers.length ? (
                <Button variant="primary" icon={<Plus />} onClick={() => setDrawer({kind: 'model', draft: {status: 1}})}>
                  新增模型
                </Button>
              ) : undefined
            }
          />
        )}
      </Panel>

      {drawer?.kind === 'model' && (
        <Drawer
          title={drawer.draft.id ? '编辑对外模型' : '新增对外模型'}
          subtitle="对外模型名称是业务系统的唯一入口，上游模型由线路决定。"
          onClose={() => setDrawer(null)}
        >
          <ModelForm value={drawer.draft} providers={providers} onSave={v => void saveModel(v)} onCancel={() => setDrawer(null)} />
        </Drawer>
      )}

      {drawer?.kind === 'route' && (
        <Drawer
          title={drawer.draft.id ? '编辑线路' : '新增备用线路'}
          subtitle={`对外模型 ${drawer.model.alias}`}
          onClose={() => setDrawer(null)}
        >
          <RouteForm
            value={drawer.draft}
            model={drawer.model}
            providers={providers}
            onSave={(m, v) => void saveRoute(m, v)}
            onCancel={() => setDrawer(null)}
          />
        </Drawer>
      )}

      {drawer?.kind === 'test' && <ModelTestDrawer models={models} initialModel={drawer.model} onClose={() => setDrawer(null)} />}
    </div>
  )
}
