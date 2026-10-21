import {ArrowLeft, LogOut, ShieldAlert} from 'lucide-react'
import {Button} from '../components/ui/Button'
import type {Workspace} from '../app/routing'

/**
 * The screen an account sees when it addresses a workspace it may not use.
 *
 * This is deliberately not a redirect. A redirect would rewrite the URL the
 * account asked for and, for an account that follows a bookmarked link, hide
 * why it was refused. The page keeps the address and offers one explicit way
 * out, so there is no redirect loop.
 */
export function WorkspaceForbidden({
  workspace,
  scope = 'workspace',
  onHome,
  onSignOut,
  sessionMode,
}: {
  workspace: Workspace
  /** A denied page inside a usable workspace reads differently from a denied workspace. */
  scope?: 'workspace' | 'page'
  onHome: () => void
  onSignOut: () => void
  sessionMode: boolean
}) {
  const target = workspace === 'admin' ? '管理控制台' : '个人控制台'
  return (
    <div className="authPage">
      <div className="authCard authCenter">
        <div className="brandMark lg" aria-hidden>
          AI
        </div>
        <ShieldAlert aria-hidden />
        <h1 className="authTitle">没有访问权限</h1>
        <p className="authHint">
          {scope === 'page'
            ? '当前账号的角色不能查看这个页面。如需访问，请联系校园信息化管理部门调整角色。'
            : `当前账号不能使用${target}。管理控制台仅对持有管理角色的账号开放；个人控制台对已登录账号开放，但需要账号具备稳定的身份。如需访问，请联系校园信息化管理部门调整角色。`}
        </p>
        <Button variant="primary" className="authSubmit" icon={<ArrowLeft />} onClick={onHome}>
          前往可用的工作台
        </Button>
        {sessionMode && (
          <Button variant="secondary" className="authSubmit" icon={<LogOut />} onClick={onSignOut}>
            退出登录
          </Button>
        )}
      </div>
    </div>
  )
}

/** The screen for a path that is not an addressable page in either workspace. */
export function WorkspaceNotFound({onHome}: {onHome: () => void}) {
  return (
    <div className="authPage">
      <div className="authCard authCenter">
        <div className="brandMark lg" aria-hidden>
          AI
        </div>
        <h1 className="authTitle">页面不存在</h1>
        <p className="authHint">这个地址不属于管理控制台或个人控制台中的任何页面。</p>
        <Button variant="primary" className="authSubmit" icon={<ArrowLeft />} onClick={onHome}>
          返回概览
        </Button>
      </div>
    </div>
  )
}
