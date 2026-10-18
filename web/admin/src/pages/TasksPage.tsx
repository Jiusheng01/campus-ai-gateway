import {Info} from 'lucide-react'
import {Panel} from '../components/ui/Panel'
import {EmptyState} from '../components/ui/EmptyState'

/**
 * Task log.
 *
 * The gateway is a synchronous proxy: a request is answered on the connection
 * that made it, so there is nothing to list here yet. The page exists because
 * the navigation promises it, and a promised page that 404s is worse than one
 * that says why it is empty.
 *
 * It deliberately does not show a table of sample rows. A task log with
 * invented entries is indistinguishable from a working one at a glance, and the
 * first person to trust it would be trusting nothing.
 */
export function TasksPage() {
  return (
    <div className="stack">
      <Panel title="任务记录" sub="异步任务的提交、执行与结果。">
        <EmptyState
          title="暂无任务数据源"
          description="当前网关是同步代理：请求在发起它的连接上直接返回，不产生独立的异步任务，因此这里还没有可展示的记录。"
        />
      </Panel>

      <Panel title="为什么是空的" sub="这一页没有接入后端接口，原因不是缺数据，而是缺数据源。">
        <div className="notice">
          <Info aria-hidden />
          <div>
            <b>本页尚未接入后端接口。</b>
            <p className="hint">
              任务日志需要一个独立的任务表来记录提交方、任务类型、状态流转和结果地址。当前
              <code>gateway_request_log</code> 只记录同步调用的耗时与用量，无法还原一个异步任务的生命周期，
              所以不做近似展示，也不拿使用日志冒充任务。
            </p>
          </div>
        </div>
      </Panel>
    </div>
  )
}
