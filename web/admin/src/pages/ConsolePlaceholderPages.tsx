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

export function ConsoleUsagePage() {
  return (
    <ComingSoon
      title="用量"
      description="按时间段查看你自己的请求数、成功与失败、输入与输出 Tokens，以及按模型分组的用量。"
    />
  )
}
