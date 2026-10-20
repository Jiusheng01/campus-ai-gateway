import {useState} from 'react'
import {CheckCircle2, Circle, Copy, X} from 'lucide-react'
import type {OnboardingGuide} from '../../api'
import type {Page} from '../../app/navigation'
import {Panel} from '../ui/Panel'
import {Button} from '../ui/Button'
import {useToast} from '../ui/Toast'
import {copyText} from '../../lib/format'

/**
 * The dashboard's setup checklist.
 *
 * Every step is answered by the deployment itself, so this is a to-do list over
 * real state rather than a tour: a step ticks itself when its row exists, and
 * goes back to pending when the last one is removed. The final step has no
 * button because it cannot be completed from the console — only a real request
 * with a real key can, so it shows the exact command instead.
 */
export function SetupGuide({
  guide,
  onNavigate,
  onHide,
}: {
  guide: OnboardingGuide
  onNavigate: (page: Page) => void
  onHide: () => void
}) {
  const toast = useToast()
  // A finished deployment should not open on a checklist every morning, so a
  // complete guide collapses to one line until someone asks to see the steps.
  const [open, setOpen] = useState(!guide.complete)
  const total = guide.steps.length
  const done = guide.steps.filter(s => s.done).length
  const pct = total === 0 ? 100 : Math.round((done / total) * 100)

  const copyCurl = async (command: string) => {
    const ok = await copyText(command)
    if (ok) toast.success('已复制，替换密钥后即可执行')
    else toast.error('复制失败，请手动选择命令')
  }

  return (
    <Panel
      title="快速开始"
      sub={guide.complete ? '全部步骤已完成，业务可以直接接入。' : '按顺序完成这几步，业务就能通过网关调用模型。'}
      actions={
        <Button size="sm" variant="ghost" icon={<X />} onClick={onHide}>
          隐藏
        </Button>
      }
    >
      <div className="setupProgress" aria-label={`已完成 ${done} / ${total} 步`}>
        <span className="setupProgressText">
          {done} / {total}
        </span>
        <span className="setupProgressBar" role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done}>
          <span style={{width: `${pct}%`}} />
        </span>
        <span className="setupProgressPct">{pct}%</span>
      </div>

      {guide.complete && (
        <div className="setupComplete">
          <CheckCircle2 aria-hidden />
          <span>配置完成。下一步是把密钥交给业务系统，或到「API 密钥」调整模型白名单。</span>
          <Button size="sm" variant="ghost" onClick={() => setOpen(v => !v)}>
            {open ? '收起步骤' : '查看步骤'}
          </Button>
        </div>
      )}

      {(!guide.complete || open) && (
        <ol className="setupSteps">
          {guide.steps.map(step => {
            const Icon = step.done ? CheckCircle2 : Circle
            const action = step.action as Page | undefined
            return (
              <li className={`setupStep ${step.done ? 'done' : ''}`.trim()} key={step.id}>
                <span className="setupStepMark" aria-hidden>
                  <Icon />
                </span>
                <div className="setupStepBody">
                  <div className="setupStepHead">
                    <b>{step.label}</b>
                    {step.count > 0 && (
                      <span className="setupStepCount">
                        {step.id === 'request' ? `${step.count} 次` : `${step.count} 项`}
                      </span>
                    )}
                    {step.done && <span className="setupStepDone">已完成</span>}
                  </div>
                  <p>{step.description}</p>
                  {step.id === 'request' && !step.done && guide.sample_model && (
                    <RequestSample model={guide.sample_model} onCopy={copyCurl} />
                  )}
                </div>
                {action && (
                  <Button size="sm" variant={step.done ? 'ghost' : 'secondary'} onClick={() => onNavigate(action)}>
                    {step.done ? '查看' : '去配置'}
                  </Button>
                )}
              </li>
            )
          })}
        </ol>
      )}
    </Panel>
  )
}

function RequestSample({model, onCopy}: {model: string; onCopy: (command: string) => void}) {
  const origin = window.location.origin
  const body = JSON.stringify({
    model,
    messages: [{role: 'user', content: '你好'}],
    stream: false,
  })
  const command = [
    `curl ${origin}/v1/chat/completions \\`,
    '  -H "Authorization: Bearer sk-campus-..." \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '${body.replace(/'/g, `'\\''`)}'`,
  ].join('\n')

  return (
    <div className="setupCurl">
      <div className="setupCurlHead">
        <span>用「{model}」发一次请求</span>
        <Button size="sm" variant="ghost" icon={<Copy />} onClick={() => onCopy(command)}>
          复制
        </Button>
      </div>
      <pre>
        <code>{command}</code>
      </pre>
      <small>把 sk-campus-... 换成「API 密钥」里创建的密钥，执行后到「使用日志」确认记录。</small>
    </div>
  )
}
