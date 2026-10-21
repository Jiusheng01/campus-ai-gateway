import {useState} from 'react'
import {Check, CheckCircle2, Copy, KeyRound, ShieldAlert} from 'lucide-react'
import {Modal} from '../ui/Modal'
import {Button} from '../ui/Button'
import {useConfirm} from '../ui/ConfirmDialog'
import {useToast} from '../ui/Toast'
import {copyText} from '../../lib/format'

/**
 * One-time reveal of a newly created API key.
 *
 * The plaintext exists only in this component's props and never touches
 * localStorage, sessionStorage or IndexedDB. The dialog refuses to close until
 * the operator ticks the acknowledgement, because the key cannot be shown
 * again once it is gone.
 */
export function SecretModal({value, onClose}: {value: string; onClose: () => void}) {
  const toast = useToast()
  const confirm = useConfirm()
  const [acknowledged, setAcknowledged] = useState(false)
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    if (await copyText(value)) {
      setCopied(true)
      toast.success('API Key 已复制到剪贴板')
    } else {
      toast.error('复制失败，请手动选择并复制')
    }
  }

  const guardedClose = async () => {
    if (acknowledged) {
      onClose()
      return
    }
    const ok = await confirm({
      title: '尚未确认已保存 API Key',
      description: '完整 API Key 只在创建完成时显示一次，关闭后将永远无法再次查看。确定要关闭吗？',
      confirmText: '仍然关闭',
      cancelText: '返回保存',
      danger: true,
    })
    if (ok) onClose()
  }

  return (
    <Modal
      title="API Key 创建成功"
      subtitle="完整 API Key 仅显示一次，离开页面后将无法再次查看。"
      dismissable={false}
      onClose={() => void guardedClose()}
      footer={
        <>
          <Button onClick={() => void guardedClose()}>关闭</Button>
          <Button
            variant="primary"
            disabled={!acknowledged}
            icon={<Check />}
            onClick={onClose}
          >
            完成并关闭
          </Button>
        </>
      }
    >
      <div className="secretModalContent">
        {/* Success Banner */}
        <div className="secretSuccessBanner">
          <CheckCircle2 className="secretSuccessIcon" aria-hidden />
          <div className="secretSuccessText">
            <b>API Key 已成功生成</b>
            <p>请立即复制并妥善配置到业务系统的环境变量中。</p>
          </div>
        </div>

        {/* Secret Key Display Box */}
        <div className="secretCodeBox">
          <div className="secretBoxHeader">
            <span className="secretKeyLabel">
              <KeyRound aria-hidden /> 完整 API Key
            </span>
            <span className="secretLengthNotice">共 {value.length} 位字符</span>
          </div>
          <div className="secretValueRow">
            <code className="secretValueText mono" title="完整 API Key">
              {value}
            </code>
            <Button
              variant={copied ? 'secondary' : 'primary'}
              size="sm"
              icon={copied ? <Check /> : <Copy />}
              onClick={() => void copy()}
            >
              {copied ? '已复制' : '复制 API Key'}
            </Button>
          </div>
        </div>

        {/* Security Alert Notice */}
        <div className="secretSecurityNotice" role="alert">
          <ShieldAlert className="secretAlertIcon" aria-hidden />
          <div>
            <b>安全防护提示：</b>
            <p>
              出于安全规范，校园网关系统数据库仅持久化保存该 API Key 的前缀及不可逆的 HMAC 摘要。
              关闭本对话框后，任何管理员都无法再次找回完整 API Key。若遗失，只能吊销并重新创建。
            </p>
          </div>
        </div>

        {/* Compulsory Acknowledgement Checkbox */}
        <label className="secretAckLabel">
          <input
            type="checkbox"
            checked={acknowledged}
            onChange={e => setAcknowledged(e.target.checked)}
          />
          <span className="ackCheckText">
            <b>我已确认复制并安全妥善保存了此 API Key</b>
          </span>
        </label>
      </div>
    </Modal>
  )
}
