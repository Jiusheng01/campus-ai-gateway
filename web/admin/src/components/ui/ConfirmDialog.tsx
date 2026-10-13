import {createContext, useCallback, useContext, useState, type ReactNode} from 'react'
import {Modal} from './Modal'
import {Button} from './Button'

export type ConfirmOptions = {
  title: string
  description?: ReactNode
  confirmText?: string
  cancelText?: string
  /** Renders the confirm button in the danger style and locks the scrim. */
  danger?: boolean
}

type Resolver = (ok: boolean) => void

const ConfirmCtx = createContext<(o: ConfirmOptions) => Promise<boolean>>(() => Promise.resolve(false))

/** Promise-based replacement for window.confirm(). */
export const useConfirm = () => useContext(ConfirmCtx)

export function ConfirmProvider({children}: {children: ReactNode}) {
  const [pending, setPending] = useState<{opts: ConfirmOptions; resolve: Resolver} | null>(null)

  const confirm = useCallback(
    (opts: ConfirmOptions) => new Promise<boolean>(resolve => setPending({opts, resolve})),
    [],
  )

  const settle = (ok: boolean) => {
    pending?.resolve(ok)
    setPending(null)
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
              <Button variant={pending.opts.danger ? 'danger' : 'primary'} autoFocus onClick={() => settle(true)}>
                {pending.opts.confirmText || '确认'}
              </Button>
            </>
          }
        >
          <span className="srOnly">请确认该操作</span>
        </Modal>
      )}
    </ConfirmCtx.Provider>
  )
}
