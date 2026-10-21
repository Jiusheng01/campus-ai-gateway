import {Building2, ShieldCheck} from 'lucide-react'
import {getConsoleMe} from '../api'
import {PageBanner} from '../components/ui/PageBanner'
import {Panel} from '../components/ui/Panel'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {useResource} from '../lib/useResource'
import {roleLabel} from '../lib/roles'

/**
 * The personal workspace's landing page.
 *
 * Every fact on it comes from /api/console/me: who the account is, the Tenant
 * it spends against, and whether that membership exists yet. The unassigned
 * state is stated plainly rather than dressed up as a zero, because an Account
 * without a Tenant has no workspace to use yet — that is a different fact from
 * "you have used nothing".
 */
export function ConsoleOverviewPage() {
  const {data, loading, error, errorStatus, reload} = useResource(getConsoleMe)

  const roles = data?.roles?.length ? data.roles : ['member']
  const displayName = data?.display_name || data?.username || ''

  return (
    <div className="stack">
      <PageBanner
        title={displayName ? `你好，${displayName}` : '个人控制台'}
        subtitle="这里汇总你的账号、租户归属与个人工作台状态。"
        illustration={false}
      />

      {error ? (
        <Panel title="概览">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : loading && !data ? (
        <Panel title="概览">
          <SkeletonRows rows={4} columns={2} />
        </Panel>
      ) : data ? (
        <>
          <Panel title="账号" sub="当前登录的身份与角色。">
            <div className="panelPad">
              <dl className="accountFacts">
                <div>
                  <dt>账号</dt>
                  <dd>{data.username}</dd>
                </div>
                <div>
                  <dt>显示名称</dt>
                  <dd>{data.display_name || '—'}</dd>
                </div>
                <div>
                  <dt>角色</dt>
                  <dd>{roles.map(roleLabel).join('、')}</dd>
                </div>
                <div>
                  <dt>账号状态</dt>
                  <dd>
                    <span className={`badge ${data.status === 1 ? 'success' : 'danger'}`}>
                      {data.status === 1 ? '正常' : '已停用'}
                    </span>
                  </dd>
                </div>
              </dl>
            </div>
          </Panel>

          <Panel title="租户与工作台" sub="租户决定你使用的配额与可调用的模型。">
            {data.workspace_usable ? (
              <div className="panelPad">
                <dl className="accountFacts">
                  <div>
                    <dt>所属租户</dt>
                    <dd>{data.tenant_name || data.tenant_id}</dd>
                  </div>
                  <div>
                    <dt>工作台状态</dt>
                    <dd>
                      <span className="badge success">可用</span>
                    </dd>
                  </div>
                </dl>
                <p className="hint">
                  你的账号已加入租户。API Key、模型与用量页面将在本次改造的后续步骤中开放。
                </p>
              </div>
            ) : (
              <EmptyState
                icon={<Building2 />}
                title="尚未分配租户"
                description="你的账号还没有加入任何租户，因此暂时没有可使用的配额。请联系管理员在「用户」中为你的账号分配租户；分配后刷新本页即可看到工作台状态。"
              />
            )}
          </Panel>

          <Panel title="接下来" sub="本次改造会逐步补齐的个人能力。">
            <div className="panelPad">
              <div className="accountNotice">
                <ShieldCheck aria-hidden />
                <div>
                  <b>概览与身份已经可用。</b>
                  <p>
                    API Key、模型、用量与调用日志页面将在本次改造的后续步骤中上线；在此之前，
                    这些入口只说明即将提供的能力，不会显示任何估算数字。
                  </p>
                </div>
              </div>
            </div>
          </Panel>
        </>
      ) : null}
    </div>
  )
}
