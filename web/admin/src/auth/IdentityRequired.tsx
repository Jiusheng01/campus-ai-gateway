import {RefreshCw} from 'lucide-react'
import {Button} from '../components/ui/Button'

/**
 * trusted-header failure screen.
 *
 * There is no local credential to enter: the campus reverse proxy injects the
 * identity, so the only useful action is to finish the campus login and retry.
 */
export function IdentityRequired({onRetry}: {onRetry: () => void}) {
  return (
    <div className="authPage">
      <div className="authCard authCenter">
        <div className="brandMark lg" aria-hidden>
          AI
        </div>
        <h1 className="authTitle">统一身份认证未完成</h1>
        <p className="authHint">
          当前网关使用学校统一身份认证。未能取得有效的管理身份，可能是统一身份认证尚未完成，
          或当前账号不具备管理权限。请完成校园统一身份认证后重试，或联系校园信息化管理部门。
        </p>
        <Button variant="primary" className="authSubmit" icon={<RefreshCw />} onClick={onRetry}>
          重新检查
        </Button>
      </div>
    </div>
  )
}
