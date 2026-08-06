import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, NetworkError } from '@/api/client'

/**
 * Async data fetching with an explicit state machine.
 *
 * SRS §4.1 requires loading, error and empty states. Modelling them as one discriminated
 * union rather than three loose booleans makes the impossible combinations
 * (`loading && error`) unrepresentable, so the UI never has to guess which to render.
 */

export type AsyncState<T> =
  | { status: 'idle'; data: null; error: null }
  | { status: 'loading'; data: null; error: null }
  | { status: 'success'; data: T; error: null }
  | { status: 'error'; data: null; error: Error }

/**
 * An intersection, not an interface extending Omit<AsyncState<T>, 'status'>.
 *
 * That distinction is the whole point: intersecting with a union distributes over its
 * members, so `status` stays a discriminant and `if (r.status === 'success')` narrows
 * `data` to `T`. Flattening it into one interface would widen `data` to `T | null`
 * permanently and force a non-null assertion at every call site — losing exactly the
 * safety the state machine was modelled for.
 */
export type UseAsyncResult<T> = AsyncState<T> & {
  isLoading: boolean
  isError: boolean
  isSuccess: boolean
  /** Re-runs the fetch. Safe to pass straight to an onClick. */
  reload: () => void
}

/**
 * Runs `fn` on mount and whenever `deps` change.
 *
 * Two correctness details:
 *  - The request is aborted on unmount, and a late response from a superseded request is
 *    discarded. Without that, switching pages quickly can let an older response overwrite
 *    a newer one — the classic race that shows stale data.
 *  - `fn` is held in a ref so callers can pass an inline arrow function without it
 *    re-triggering the effect on every render.
 */
export function useAsync<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[] = [],
): UseAsyncResult<T> {
  const [state, setState] = useState<AsyncState<T>>({ status: 'idle', data: null, error: null })

  const fnRef = useRef(fn)
  fnRef.current = fn

  // Bumped on every run; a response whose id is no longer current is dropped.
  const runId = useRef(0)

  const run = useCallback(() => {
    const id = ++runId.current
    const controller = new AbortController()

    setState({ status: 'loading', data: null, error: null })

    fnRef
      .current(controller.signal)
      .then((data) => {
        if (id !== runId.current) return // superseded
        setState({ status: 'success', data, error: null })
      })
      .catch((err: unknown) => {
        if (id !== runId.current) return
        // An abort is our own doing, not a failure to report.
        if (err instanceof DOMException && err.name === 'AbortError') return
        setState({ status: 'error', data: null, error: normalizeError(err) })
      })

    return () => controller.abort()
  }, [])

  useEffect(() => {
    const cancel = run()
    return cancel
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return {
    ...state,
    isLoading: state.status === 'loading',
    isError: state.status === 'error',
    isSuccess: state.status === 'success',
    reload: run,
  }
}

/**
 * Async state for user-triggered actions (submit, approve, settle).
 *
 * Separate from `useAsync` because the trigger is an event, not a render: this must not
 * run on mount, and it returns the result so the caller can navigate or refresh after.
 */
export interface UseActionResult<TArgs extends unknown[], TResult> {
  run: (...args: TArgs) => Promise<TResult | undefined>
  isPending: boolean
  error: Error | null
  reset: () => void
}

export function useAction<TArgs extends unknown[], TResult>(
  fn: (...args: TArgs) => Promise<TResult>,
): UseActionResult<TArgs, TResult> {
  const [isPending, setPending] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const fnRef = useRef(fn)
  fnRef.current = fn

  const run = useCallback(async (...args: TArgs): Promise<TResult | undefined> => {
    setPending(true)
    setError(null)
    try {
      const result = await fnRef.current(...args)
      return result
    } catch (err: unknown) {
      // Guard against setting state after the component unmounted — for example when the
      // action navigates away and then rejects.
      if (mounted.current) setError(normalizeError(err))
      return undefined
    } finally {
      if (mounted.current) setPending(false)
    }
  }, [])

  const reset = useCallback(() => setError(null), [])

  return { run, isPending, error, reset }
}

/** Ensures the UI always has a real Error with a message worth showing. */
export function normalizeError(err: unknown): Error {
  if (err instanceof ApiError || err instanceof NetworkError) return err
  if (err instanceof Error) return err
  return new Error(typeof err === 'string' ? err : 'Terjadi kesalahan yang tidak terduga.')
}
