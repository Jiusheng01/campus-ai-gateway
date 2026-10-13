import {createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode} from 'react'
import {AlertTriangle, CheckCircle2, Info, XCircle} from 'lucide-react'

export type ToastTone = 'success' | 'error' | 'warning' | 'info'

type ToastItem = {id: number; tone: ToastTone; text: string}

type Api = {
  success: (text: string) => void
  error: (text: string) => void
  warning: (text: string) => void
  info: (text: string) => void
}

const ToastCtx = createContext<Api>({
  success: () => {},
  error: () => {},
  warning: () => {},
  info: () => {},
})

export const useToast = () => useContext(ToastCtx)

// Errors linger: an operator who looks away for two seconds should still see
// why the action failed. Successes can disappear quickly.
const LIFETIME: Record<ToastTone, number> = {success: 2600, info: 3200, warning: 4200, error: 6000}

const ICONS = {
  success: <CheckCircle2 />,
  error: <XCircle />,
  warning: <AlertTriangle />,
  info: <Info />,
} as const

export function ToastProvider({children}: {children: ReactNode}) {
  const [items, setItems] = useState<ToastItem[]>([])

  const push = useCallback((tone: ToastTone, text: string) => {
    setItems(prev => [...prev.slice(-3), {id: Date.now() + Math.random(), tone, text}])
  }, [])

  const api = useMemo<Api>(
    () => ({
      success: (t: string) => push('success', t),
      error: (t: string) => push('error', t),
      warning: (t: string) => push('warning', t),
      info: (t: string) => push('info', t),
    }),
    [push],
  )

  return (
    <ToastCtx.Provider value={api}>
      {children}
      <div className="toastWrap" aria-live="polite">
        {items.map(item => (
          <ToastCard key={item.id} item={item} onDone={() => setItems(prev => prev.filter(x => x.id !== item.id))} />
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

function ToastCard({item, onDone}: {item: ToastItem; onDone: () => void}) {
  useEffect(() => {
    const t = setTimeout(onDone, LIFETIME[item.tone])
    return () => clearTimeout(t)
  }, [item.tone, onDone])

  return (
    <div className={`toast ${item.tone}`} role={item.tone === 'error' ? 'alert' : 'status'}>
      <span className="toastIcon" aria-hidden>
        {ICONS[item.tone]}
      </span>
      <span className="toastText">{item.text}</span>
      <button type="button" className="toastClose" onClick={onDone} aria-label="关闭提示" title="关闭提示">
        ✕
      </button>
    </div>
  )
}
