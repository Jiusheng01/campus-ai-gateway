import {useCallback, useEffect, useRef, useState} from 'react'

export type Resource<T> = {
  data: T | null
  loading: boolean
  error: string
  /** HTTP status behind `error`, when the failure came from the API. */
  errorStatus: number
  reload: () => void
}

/**
 * Loads a value once per dependency change and exposes a manual reload.
 *
 * Pages own their own data instead of pushing everything through a single
 * top-level loader: a failed request on one page no longer blanks the others,
 * and each page can show its own skeleton and retry affordance.
 */
export function useResource<T>(loader: () => Promise<T>, deps: unknown[] = []): Resource<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [errorStatus, setErrorStatus] = useState(0)
  const [nonce, setNonce] = useState(0)
  const alive = useRef(true)
  const loaderRef = useRef(loader)
  loaderRef.current = loader

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    setErrorStatus(0)
    loaderRef
      .current()
      .then(v => {
        if (!cancelled) setData(v)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setError(e instanceof Error ? e.message : String(e))
        // ApiError carries the status; anything else is a network-level failure.
        const status = (e as {status?: number})?.status
        setErrorStatus(typeof status === 'number' ? status : 0)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce])

  const reload = useCallback(() => setNonce(n => n + 1), [])
  return {data, loading, error, errorStatus, reload}
}
