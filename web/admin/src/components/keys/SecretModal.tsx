import {useState} from 'react'
import {AlertTriangle, Check, Copy, KeyRound} from 'lucide-react'
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
      description: '完整 Key 只显示一次，关闭后无法再次查看。确定要关闭吗？',
      confirmText: '仍然关闭',
      cancelText: '返回保存',
      danger: true,
    })
    if (ok) onClose()
  }

  return (
    <Modal
      title="API Key 创建成功"
      subtitle="完整 Key 只显示一次，关闭后无法再次查看。"
      dismissable={false}
      onClose={() => void guardedClose()}
      footer={
        <>
          <Button onClick={() => void guardedClose()}>关闭</Button>
          <Button variant="primary" disabled={!acknowledged} icon={<Check />} onClick={onClose}>
            完成
          </Button>
        </>
      }
    >
      <div className="secretBox">
        <KeyRound aria-hidden />
        <p>请立即复制并妥善保存到业务系统的密钥管理位置。</p>
        <code className="secretValue">{value}</code>
        <Button icon={<Copy />} onClick={() => void copy()}>
          {copied ? '已复制' : '复制 API Key'}
        </Button>
      </div>

      <label className="ackRow">
        <input type="checkbox" checked={acknowledged} onChange={e => setAcknowledged(e.target.checked)} />
        <span>我已经安全保存此 Key</span>
      </label>

      <div className="notice warning">
        <AlertTriangle aria-hidden />
        该 Key 不会写入浏览器本地存储，刷新或关闭后无法再次查看。
      </div>
    </Modal>
  )
}
