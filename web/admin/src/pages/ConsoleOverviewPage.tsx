import {useCallback, useState, type ReactNode} from 'react'
import {Building2, FileClock, KeyRound, LineChart} from 'lucide-react'
import {
  fetchConsoleUsage,
  getConsoleMe,
  getConsoleModels,
  listConsoleKeys,
  listConsoleRequestLogs,
  type ConsoleMe,
  type ConsoleModels,
  type ConsoleRequestLog,
  type ConsoleRequestLogPage,
} from '../api'
import {PageBanner} from '../components/ui/PageBanner'
import {Panel} from '../components/ui/Panel'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {StatCard, StatGrid} from '../components/ui/StatCard'
import {Table} from '../components/ui/Table'
import {HttpStatusBadge} from '../components/ui/Badge'
import {TrendChart} from '../components/charts/TrendChart'
import {useResource, type Resource} from '../lib/useResource'
import {ms, num, when} from '../lib/format'
import {roleLabel} from '../lib/roles'
import {pathFor} from '../app/routing'
import type {Page} from '../app/navigation'

type Navigate = (page: Page) => void

/**
 * A real link that stays inside the SPA when the shell is available.
 *
 * The href is always present, so a bookmark, a middle click and a page rendered
 * without the shell (the page tests) all resolve to a real address. A plain
 * left click is intercepted only when a navigate callback was supplied.
 */
function ConsoleLink({
  page,
  onNavigate,
  className = '',
  children,
}: {
  page: Page
  onNavigate?: Navigate
  className?: string
  children: ReactNode
}) {
  return (
    <a
      className={className}
      href={pathFor('console', page)}
      onClick={event => {
        if (!onNavigate) return
        if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
        event.preventDefault()
        onNavigate(page)
      }}
    >
      {children}
    </a>
  )
}

/**
 * The personal workspace's landing page.
 *
 * Every fact on it comes from /api/console/*: /me names the account and its
 * Tenant Membership, /keys counts the credentials the account owns, /usage
 * answers today's requests, Tokens and error rate, /request-logs supplies the
 * last few records, and /models supplies the catalog count. Nothing is a
 * placeholder, and the three empty states are kept apart because they ask for
 * three different actions.
 *
 * The summary below only mounts when /me reports a usable workspace. An
 * unassigned account therefore never sees a zero rendered as a measurement: it
 * sees the assignment it is waiting for instead.
 */
export function ConsoleOverviewPage({onNavigate}: {onNavigate?: Navigate} = {}) {
  const {data, loading, error, errorStatus, reload} = useResource(getConsoleMe)

  const roles = data?.roles?.length ? data.roles : ['member']
  const displayName = data?.display_name || data?.username || ''

  return (
    <div className="stack">
      <PageBanner
        title={displayName ? `你好，${displayName}` : '个人控制台'}
        subtitle="你的账号、租户归属、今日用量与最近调用，一页汇总。"
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
          <AccountPanel data={data} roles={roles} />

          {data.workspace_usable ? (
            <>
              <Panel title="租户与工作台" sub="租户决定你使用的配额与可调用的模型。">
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
                </div>
              </Panel>

              <WorkspaceSummary onNavigate={onNavigate} />
            </>
          ) : (
            <Panel title="租户与工作台" sub="租户决定你使用的配额与可调用的模型。">
              <EmptyState
                icon={<Building2 />}
                title="尚未分配租户"
                description="你的账号还没有加入任何租户，因此暂时没有可使用的配额。请联系管理员在「用户」中为你的账号分配租户；分配后刷新本页即可看到工作台状态。"
              />
            </Panel>
          )}
        </>
      ) : null}
    </div>
  )
}

function AccountPanel({data, roles}: {data: ConsoleMe; roles: string[]}) {
  return (
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
          <div>
            <dt>所属租户</dt>
            <dd>{data.tenant_name || '尚未分配'}</dd>
          </div>
        </dl>
      </div>
    </Panel>
  )
}

/**
 * The figures the account can act on: its keys, today's traffic and the models
 * it may call.
 *
 * The hooks live here rather than in the parent so an unassigned account does
 * not fetch a key list or a usage window it has no workspace to interpret. The
 * branch order is the three distinct empty states: no key (create one), no
 * traffic (nothing has been called yet), and finally the measured figures.
 */
function WorkspaceSummary({onNavigate}: {onNavigate?: Navigate}) {
  const keys = useResource(listConsoleKeys)
  const logs = useResource(useCallback(() => listConsoleRequestLogs('limit=5'), []))

  // "Today" is the reader's local day, and the series uses the same window, so
  // the trend beside the cards is the context for those exact figures.
  const usageLoader = useCallback(() => {
    const to = new Date()
    const from = new Date(to)
    from.setHours(0, 0, 0, 0)
    return fetchConsoleUsage(from, to, 'hour')
  }, [])
  const usage = useResource(usageLoader)
  const models = useResource(getConsoleModels)

  const [hover, setHover] = useState<number | null>(null)

  const modelEntry = (
    <ModelsEntry
      data={models.data}
      loading={models.loading && !models.data}
      failed={!!models.error && !models.data}
      onNavigate={onNavigate}
    />
  )

  if (keys.error && !keys.data) {
    return (
      <>
        <Panel title="我的 API Key">
          <ErrorState message={keys.error} status={keys.errorStatus} onRetry={keys.reload} />
        </Panel>
        {modelEntry}
      </>
    )
  }

  if (!keys.data) {
    return (
      <>
        <Panel title="概览">
          <SkeletonRows rows={4} columns={4} />
        </Panel>
        {modelEntry}
      </>
    )
  }

  const keyRows = keys.data
  const enabled = keyRows.filter(k => k.status === 1).length

  if (keyRows.length === 0) {
    return (
      <>
        <Panel title="我的 API Key" sub="个人凭据由你的账号持有，并受租户模型范围限制。">
          <EmptyState
            icon={<KeyRound />}
            title="还没有 API Key"
            description="创建你的第一个 API Key 后，这里会显示今日请求、Tokens 与错误率。个人 API Key 由你的账号持有，并受租户模型范围限制。"
            action={
              <ConsoleLink page="keys" onNavigate={onNavigate} className="btn primary sm">
                创建 API Key
              </ConsoleLink>
            }
          />
        </Panel>
        {modelEntry}
      </>
    )
  }

  if ((usage.error && !usage.data) || (usage.loading && !usage.data)) {
    return (
      <>
        <Panel title="今日概览">
          {usage.error ? (
            <ErrorState message={usage.error} status={usage.errorStatus} onRetry={usage.reload} />
          ) : (
            <SkeletonRows rows={2} columns={3} />
          )}
        </Panel>
        {modelEntry}
      </>
    )
  }

  if (logs.loading && !logs.data) {
    return (
      <>
        <Panel title="今日概览">
          <SkeletonRows rows={3} columns={5} />
        </Panel>
        {modelEntry}
      </>
    )
  }

  const totals = usage.data?.totals
  const points = usage.data?.series || []
  const records = logs.data?.data || []

  if (!totals) return modelEntry

  // No key list would already have returned above, so an empty record list here
  // means the account owns credentials but nothing has ever called with them.
  const noTraffic = !!logs.data && records.length === 0

  if (noTraffic) {
    return (
      <>
        <Panel title="今日概览" sub="今日请求、Tokens 与错误率来自你的个人调用记录。">
          <div className="panelPad">
            <StatGrid columns={2}>
              <StatCard label="我的 API Key" value={num(keyRows.length)} tone="info" subtext="你拥有的个人凭据" />
              <StatCard label="已启用 Key" value={num(enabled)} tone="success" subtext={`共 ${num(keyRows.length)} 个`} />
            </StatGrid>
            <EmptyState
              icon={<LineChart />}
              title="还没有任何调用"
              description="你的账号已经拥有 API Key，但还没有产生任何请求记录。使用个人 API Key 发起第一次调用后，今日请求、Tokens 与错误率会显示在这里。"
              action={
                <ConsoleLink page="models" onNavigate={onNavigate} className="linkInline">
                  查看调用方式
                </ConsoleLink>
              }
            />
          </div>
        </Panel>
        {modelEntry}
      </>
    )
  }

  return (
    <>
      <Panel title="今日概览" sub="今日请求、Tokens 与错误率来自你的个人调用记录；时间边界按本机时区计算。">
        <div className="panelPad">
          <StatGrid columns={3}>
            <StatCard label="我的 API Key" value={num(keyRows.length)} tone="info" subtext="你拥有的个人凭据" />
            <StatCard label="已启用 Key" value={num(enabled)} tone="success" subtext={`共 ${num(keyRows.length)} 个`} />
            <StatCard label="今日请求" value={num(totals.requests)} tone="info" subtext={`成功 ${num(totals.successes)} · 失败 ${num(totals.failures)}`} />
            <StatCard label="今日 Token" value={num(totals.total_tokens)} tone="purple" subtext={`输入 ${num(totals.input_tokens)} · 输出 ${num(totals.output_tokens)}`} />
            <StatCard
              label="今日错误率"
              value={`${totals.error_rate.toFixed(2)}%`}
              tone={totals.failures > 0 ? 'danger' : 'default'}
              subtext="失败请求占今日请求的比例"
            />
          </StatGrid>
        </div>
      </Panel>

      <Panel title="今日调用趋势" sub="按小时聚合的请求数，红色段为失败请求。">
        <div className="panelPad">
          <TrendChart
            points={points}
            value={p => p.requests}
            format={num}
            metric="请求数"
            withErrors
            hover={hover}
            onHover={setHover}
          />
        </div>
      </Panel>

      <Panel
        title="最近的请求"
        sub="你最近发起的调用记录。"
        actions={
          <ConsoleLink page="logs" onNavigate={onNavigate} className="linkInline">
            查看全部
          </ConsoleLink>
        }
      >
        <RecentRecords logs={logs} records={records} />
      </Panel>

      {modelEntry}
    </>
  )
}

function RecentRecords({
  logs,
  records,
}: {
  logs: Resource<ConsoleRequestLogPage>
  records: ConsoleRequestLog[]
}) {
  if (logs.error && !logs.data) {
    return <ErrorState message={logs.error} status={logs.errorStatus} onRetry={logs.reload} />
  }

  if (!records.length) {
    return (
      <EmptyState
        icon={<FileClock />}
        title="还没有调用记录"
        description="使用你的个人 API Key 发起调用后，最近的记录会显示在这里。"
      />
    )
  }

  return (
    <Table heads={['时间', '模型', 'Tokens', '延迟', '状态']} minWidth={620}>
      {records.map(r => (
        <tr key={r.request_id}>
          <td>{when(r.created_at)}</td>
          <td>
            <b>{r.requested_model || '未知'}</b>
            {r.actual_model && r.actual_model !== r.requested_model && (
              <small className="muted"> 实际 {r.actual_model}</small>
            )}
          </td>
          <td className="numeric">{num(r.total_tokens)}</td>
          <td className="numeric">{ms(r.latency_ms)}</td>
          <td>
            <HttpStatusBadge code={r.status_code} />
          </td>
        </tr>
      ))}
    </Table>
  )
}

/**
 * The models entry point. It carries `models.length`, never a hard-coded count,
 * and when the catalog is empty it names the reason the API supplied: a Tenant
 * that permits nothing, an empty deployment, or an unassigned account.
 */
function ModelsEntry({
  data,
  loading,
  failed,
  onNavigate,
}: {
  data: ConsoleModels | null
  loading: boolean
  failed: boolean
  onNavigate?: Navigate
}) {
  return (
    <Panel
      title="模型"
      sub="你的租户允许调用的模型与接入方式。"
      actions={
        <ConsoleLink page="models" onNavigate={onNavigate} className="linkInline">
          查看模型
        </ConsoleLink>
      }
    >
      <div className="panelPad">
        <p className="hint">{modelsEntryText(data, loading, failed)}</p>
      </div>
    </Panel>
  )
}

function modelsEntryText(data: ConsoleModels | null, loading: boolean, failed: boolean): string {
  if (loading) return '正在读取模型目录…'
  if (failed || !data) return '模型目录暂时无法读取，请稍后刷新本页。'

  const count = data.models?.length || 0
  if (count > 0) return `你的租户当前允许调用 ${num(count)} 个模型。`

  switch (data.empty_reason) {
    case 'no_tenant':
      return '尚未分配租户，因此没有可调用的模型。请联系管理员为你的账号分配租户。'
    case 'tenant_allows_none':
      return '你的租户当前未开放任何模型。请联系管理员在租户设置中开放所需模型。'
    case 'no_eligible_models':
      return '你的租户没有限制模型，但网关当前还没有可调用的模型线路。请联系管理员启用模型或补充线路。'
    default:
      return '当前没有可调用的模型。'
  }
}
