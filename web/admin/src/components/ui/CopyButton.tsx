import {useState} from 'react'
import {Copy} from 'lucide-react'
import {Button} from './Button'
import {useToast} from './Toast'
import {copyText} from '../../lib/format'

/** Copy only a caller-provided value; secret lifetimes stay with the owning flow. */
export function CopyButton({value, label = '复制', disabled = false}: {value: string; label?: string; disabled?: boolean}) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  async function copy() {
    setBusy(true)
    if (await copyText(value)) toast.success('已复制到剪贴板')
    else toast.error('复制失败，请选择完整内容后手动复制')
    setBusy(false)
  }
  return <Button size="sm" icon={<Copy />} loading={busy} disabled={disabled} onClick={() => void copy()}>{label}</Button>
}
