import {useCallback, useState} from 'react'
import {Boxes, ChevronRight, FlaskConical, KeyRound, Plus, Route} from 'lucide-react'
import {api, listModelTestOptions, type ModelConfig, type ModelDetail, type ModelImpact, type ModelRoute, type ModelTestOption} from '../api'
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
import {useToast} from '../components/ui/Toast'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useResource} from '../lib/useResource'

/**
 * The external-model page is the client-facing view: an alias is what a business
 * system calls, while the channel and upstream model belong to its lines.
 *
 * MS1 has no model-kind contract. The page therefore says "未知" instead of
 * claiming every legacy alias is a chat model.
 */
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
    const d = await api<{data: ModelConfig[]}>('/admin/models')
    return d.data || []
  }, [])
  const {data, loading, error, errorStatus, reload} = useResource(loader)
  const [detail, setDetail] = useState<ModelDetail | null>(null)
  const toast = useToast()
  const confirm = useConfirm()
  const [detailBusy, setDetailBusy] = useState('')
  const [detailError, setDetailError] = useState('')
  const [editingRoutes, setEditingRoutes] = useState(false)
  const [impact, setImpact] = useState<ModelImpact | null>(null)
  const [impactError, setImpactError] = useState('')
  const [disableBusy, setDisableBusy] = useState(false)
  const [testing, setTesting] = useState<ModelTestOption[] | null>(null)
  const [publishing, setPublishing] = useState(false)
  // Direct authorization entry: opens the same drawer at the authorization
  // panel instead of requiring a scroll after the detail loads.
  const [focusAuthorization, setFocusAuthorization] = useState(false)

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

  const setModelStatus = async (status: 1 | 2) => {
    if (!detail) return
    setDisableBusy(true)
    try {
      await api(`/admin/models/${detail.model.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          alias: detail.model.alias,
          display_name: detail.model.display_name,
          description: detail.model.description,
          status,
          expected_version: detail.version,
        }),
      })
      toast.success(status === 2 ? '模型已停用；已发布 alias 不会自动从 Key 白名单移除' : '模型已启用')
      setImpact(null)
      await open(detail.model)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : (status === 2 ? '停用模型失败' : '启用模型失败'))
    } finally {
      setDisableBusy(false)
    }
  }

  const enableModel = async () => {
    if (!detail) return
    const ok = await confirm({
      title: `启用模型「${detail.model.alias}」？`,
      description: '启用后该 alias 会在至少一条启用线路时重新进入静态可路由集合；Key 授权不变。',
      confirmText: '启用模型',
    })
    if (ok) await setModelStatus(1)
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
        }, 50)
      }
    } catch (e) {
      setDetailError(e instanceof Error ? e.message : '无法加载模型详情')
    } finally {
      setDetailBusy('')
    }
  }

  const models = data || []

  return (
    <>
      <Panel
        title="对外模型"
        sub="业务系统只使用这里的外部名称。渠道、上游模型和故障切换都由每个模型的线路决定。"
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setPublishing(true)}>
            发布模型
          </Button>
        }
      >
        {loading && !data ? (
          <SkeletonCards count={3} />
        ) : error ? (
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        ) : models.length ? (
          <div className="modelCards">
            {models.map(m => (
              <article className={`modelCard ${m.status === 1 ? '' : 'disabled'}`} key={m.id}>
                <header className="modelHead">
                  <div className="modelIdentity">
                    <div className="modelAliasRow">
                      <h3 className="modelAlias mono">{m.alias}</h3>
                      <StatusBadge status={m.status} />
                    </div>
                    <span className="modelKind">{m.display_name || '未设置显示名称'}</span>
                    <p className="modelHint">
                      业务系统使用 <code className="mono">model={m.alias}</code>
                    </p>
                  </div>
                  <div className="modelActions">
                    {canAuthorize && (
                      <Button size="sm" icon={<KeyRound />} loading={detailBusy === m.id} onClick={() => void open(m, {authorize: true})}>
                        管理授权
                      </Button>
                    )}
                    <Button size="sm" icon={<ChevronRight />} loading={detailBusy === m.id} onClick={() => void open(m)}>
                      查看详情
                    </Button>
                  </div>
                </header>

                <dl className="providerRows">
                  <div className="providerRow">
                    <dt>配置完整性</dt>
                    <dd>
                      {m.statically_routable ? <Badge tone="success">至少一条静态可路由线路</Badge> : <Badge tone="warning">暂无静态可路由线路</Badge>}
                    </dd>
                  </div>
                  <div className="providerRow">
                    <dt>线路</dt>
                    <dd>
                      {m.enabled_route_count ?? 0} / {m.route_count ?? 0} 条启用
                      {m.routing_mode_summary ? <span className="muted"> · {m.routing_mode_summary}</span> : null}
                    </dd>
                  </div>
                  <div className="providerRow">
                    <dt>凭据</dt>
                    <dd>{m.credential_configured ? '来源已配置' : '未知或未配置'}</dd>
                  </div>
                  <div className="providerRow">
                    <dt>服务类型</dt>
                    <dd>未知（MS1 未建立类型契约）</dd>
                  </div>
                  <div className="providerRow">
                    <dt>配置版本</dt>
                    <dd className="mono">{m.config_version}</dd>
                  </div>
                </dl>
              </article>
            ))}
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

      {publishing && (
        <PublishModelWizard
          onClose={() => setPublishing(false)}
          onPublished={() => {
            void reload()
          }}
        />
      )}

      {detail && (
        <Drawer title={detail.model.alias} subtitle="对外模型详情" size="lg" onClose={() => setDetail(null)}>
          <div className="drawerStack">
            {canAuthorize && !focusAuthorization && (
              <div className="quickActions">
                <Button size="sm" onClick={() => document.getElementById('model-authorization')?.scrollIntoView({behavior: 'smooth'})}>
                  管理授权
                </Button>
              </div>
            )}
            <Detail
              title="基本信息"
              rows={[
                ['对外名称（alias）', <span key="alias" className="mono">{detail.model.alias}</span>],
                ['显示名称', detail.model.display_name || '—'],
                ['描述', detail.model.description || '—'],
                ['状态', detail.model.status === 1 ? '启用' : '停用'],
                ['配置版本', <span key="version" className="mono">{detail.version}</span>],
                ['服务类型', '未知（MS1 未建立类型契约）'],
              ]}
            />
            {detail.model.status === 1 ? (
              <Panel title="下线模型" sub="停用后该 alias 不再静态可路由；已授权的 Key 不会被自动改写。">
                <Button variant="danger" onClick={() => void loadImpact(detail.model)}>查看停用影响</Button>
              </Panel>
            ) : (
              <Panel title="上线模型" sub="启用不会修改 Key 授权；仍需至少一条启用线路才能静态可路由。">
                <Button variant="primary" onClick={() => void enableModel()}>启用模型</Button>
              </Panel>
            )}
            <Panel
              title="线路"
              sub="线路是唯一的运行时路由权威。"
              actions={<Button size="sm" onClick={() => setEditingRoutes(true)}>编辑线路</Button>}
            >
              {detail.routes.length ? (
                <div className="routeChain">
                  {detail.routes.map((r: ModelRoute) => (
                    <div className="routeBox" key={r.id}>
                      <div className="routeBoxHead">
                        <b>{r.provider_name}</b>
                        <span className="routeTags">
                          <Badge tone="info">优先级 {r.priority}</Badge>
                          <StatusBadge status={r.status} />
                        </span>
                      </div>
                      <code className="routeModel mono">{r.upstream_model}</code>
                      <div className="routeMeta">
                        <span>权重 <b>{r.weight}</b></span>
                        <span>响应头超时 <b>{r.timeout_ms} ms</b></span>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState icon={<Route />} title="暂无线路" description="该模型尚不可路由。" />
              )}
            </Panel>
            {canAuthorize && (
              <div id="model-authorization">
                <Panel title="授权" sub="授权针对具体 API Key；发布模型不会自动授权。">
                  <ModelAuthorizationPanel modelID={detail.model.id} canAuthorize />
                </Panel>
              </div>
            )}
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
            <Panel title="使用记录" sub="脱敏运行摘要；原始日志仍由超级管理员/审计员读取。">
              <RuntimeSummaryPanel
                kind="model"
                id={detail.model.id}
                canOpenRawLogs={canOpenRawLogs}
                onOpenLogs={onOpenLogs}
              />
            </Panel>
          </div>
        </Drawer>
      )}

      {impact && (
        <Modal
          title={`停用模型：${impact.alias}`}
          subtitle="这是影响预览；提交后服务端会重新校验并立即停用。"
          onClose={() => setImpact(null)}
          footer={
            <>
              <Button onClick={() => setImpact(null)}>取消</Button>
              <Button variant="danger" loading={disableBusy} onClick={() => void setModelStatus(2)}>确认停用</Button>
            </>
          }
        >
          <div className="drawerStack">
            <p>启用线路：{impact.enabled_routes} 条；启用渠道：{impact.enabled_providers} 个。</p>
            <p>显式允许该 alias 的 Key：{impact.explicit_keys} 个；通配符 Key：{impact.wildcard_keys} 个。</p>
            <p className="hint">停用不会修改任何 Key 的白名单。通配符 Key 仍允许全部模型，但该 alias 不再出现在静态可路由集合中。</p>
            <p className="hint">本实例保存后立即生效；其他实例在 5 秒 TTL 内收敛；数据库故障时可能继续使用旧快照。</p>
          </div>
        </Modal>
      )}

      {impactError && (
        <div className="notice warning" role="alert">{impactError}</div>
      )}

      {testing && (
        <ModelTestDrawer
          models={testing}
          initialModel={detail?.model.alias}
          onClose={() => setTesting(null)}
        />
      )}

      {editingRoutes && detail && (
        <Modal
          title={`编辑线路：${detail.model.alias}`}
          subtitle="整组保存，带配置版本校验。"
          onClose={() => setEditingRoutes(false)}
        >
          <RouteEditor
            modelID={detail.model.id}
            onSaved={() => {
              void open(detail.model)
              reload()
            }}
          />
        </Modal>
      )}

      {detailError && !detail && (
        <div className="notice warning" role="alert">
          {detailError}
        </div>
      )}
    </>
  )
}
