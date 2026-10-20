import {createContext, useCallback, useContext, useState, type ReactNode} from 'react'
import {Modal} from './Modal'
import {Button} from './Button'
import {Field} from './Field'

export type ConfirmOptions = {
  title: string
  description?: ReactNode
  confirmText?: string
  cancelText?: string
  /** Renders the confirm button in the danger style and locks the scrim. */
  danger?: boolean
  /**
   * When set, the operator must type exactly this text before confirming. Used
   * for irreversible actions where one click is too cheap a mistake: the cost
   * is reading the name and typing it back, not dismissing a dialog.
   */
  requireText?: string
  requireTextLabel?: string
}

type Resolver = (ok: boolean) => void

const ConfirmCtx = createContext<(o: ConfirmOptions) => Promise<boolean>>(() => Promise.resolve(false))

/** Promise-based replacement for window.confirm(). */
export const useConfirm = () => useContext(ConfirmCtx)

export function ConfirmProvider({children}: {children: ReactNode}) {
  const [pending, setPending] = useState<{opts: ConfirmOptions; resolve: Resolver} | null>(null)
  const [typed, setTyped] = useState('')

  const confirm = useCallback((opts: ConfirmOptions) => {
    setTyped('')
    return new Promise<boolean>(resolve => setPending({opts, resolve}))
  }, [])

  const required = pending?.opts.requireText
  const isTyped = required === undefined || typed === required

  const settle = (ok: boolean) => {
    if (ok && !isTyped) return
    pending?.resolve(ok)
    setPending(null)
    setTyped('')
  }

  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      {pending && (
        <Modal
          title={pending.opts.title}
          subtitle={pending.opts.description}
          onClose={() => settle(false)}
          dismissable={!pending.opts.danger}
          footer={
            <>
              <Button onClick={() => settle(false)}>{pending.opts.cancelText || '取消'}</Button>
              <Button variant={pending.opts.danger ? 'danger' : 'primary'} autoFocus={required === undefined} disabled={!isTyped} onClick={() => settle(true)}>
                {pending.opts.confirmText || '确认'}
              </Button>
            </>
          }
        >
          {required === undefined ? (
            <span className="srOnly">请确认该操作</span>
          ) : (
            <Field label={pending.opts.requireTextLabel || `请输入「${required}」以确认`}>
              <input
                value={typed}
                autoFocus
                autoComplete="off"
                spellCheck={false}
                onChange={e => setTyped(e.target.value)}
                onKeyDown={e => {
                  if (e.key === 'Enter') settle(true)
                }}
              />
            </Field>
          )}
        </Modal>
      )}
    </ConfirmCtx.Provider>
  )
}
