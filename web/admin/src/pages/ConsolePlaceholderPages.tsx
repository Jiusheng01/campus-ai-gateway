import {Construction} from 'lucide-react'
import {Panel} from '../components/ui/Panel'
import {EmptyState} from '../components/ui/EmptyState'

/**
 * Honest placeholders for the personal pages that arrive in later tickets.
 *
 * They exist so the workspace navigation is addressable and a deep link lands
 * somewhere truthful. There are deliberately no sample rows or zero-valued
 * metrics here: a placeholder must not look like a measurement.
 */
function ComingSoon({title, description}: {title: string; description: string}) {
  return (
    <div className="stack">
      <Panel title={title} sub="个人控制台">
        <EmptyState
          icon={<Construction />}
          title="将在本次改造的后续步骤中上线"
          description={description}
        />
      </Panel>
    </div>
  )
}

export function ConsoleLogsPage() {
  return (
    <ComingSoon
      title="调用日志"
      description="查看你自己发起的请求记录，包括时间、模型、Tokens、延迟与状态，并打开单条记录查看尝试链路。"
    />
  )
}
