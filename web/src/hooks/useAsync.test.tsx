import { describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useAction, useAsync } from './useAsync'
import { ApiError } from '@/api/client'

/**
 * The two hooks behind every loading/error/empty state in the app (SRS §4.1).
 *
 * The interesting cases are the races: a superseded response must not overwrite a newer
 * one, and state must not be written after unmount.
 */

function Probe({ fn, deps }: { fn: (signal: AbortSignal) => Promise<string>; deps?: unknown[] }) {
  const state = useAsync(fn, deps ?? [])
  return (
    <div>
      <span data-testid="status">{state.status}</span>
      <span data-testid="data">{state.data ?? ''}</span>
      <span data-testid="error">{state.error?.message ?? ''}</span>
      <button onClick={state.reload}>reload</button>
    </div>
  )
}

describe('useAsync', () => {
  it('moves loading → success and exposes the data', async () => {
    render(<Probe fn={async () => 'hello'} />)

    // The first paint is already 'loading', so there is never a frame with no state.
    await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('success'))
    expect(screen.getByTestId('data')).toHaveTextContent('hello')
  })

  it('moves loading → error and keeps the message', async () => {
    render(
      <Probe
        fn={async () => {
          throw new ApiError(422, 'INVALID_STATE', 'invalid state transition')
        }}
      />,
    )

    await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('error'))
    expect(screen.getByTestId('error')).toHaveTextContent('invalid state transition')
  })

  it('normalises a non-Error rejection so the UI always has a message', async () => {
    render(
      <Probe
        fn={async () => {
          // eslint-disable-next-line @typescript-eslint/only-throw-error
          throw 'plain string'
        }}
      />,
    )

    await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('error'))
    expect(screen.getByTestId('error')).toHaveTextContent('plain string')
  })

  it('re-runs on reload', async () => {
    let calls = 0
    const fn = vi.fn(async () => `call-${++calls}`)
    render(<Probe fn={fn} />)

    await waitFor(() => expect(screen.getByTestId('data')).toHaveTextContent('call-1'))
    await userEvent.click(screen.getByText('reload'))
    await waitFor(() => expect(screen.getByTestId('data')).toHaveTextContent('call-2'))
  })

  it('does not re-run when an inline function is re-created on every render', async () => {
    // The callback is held in a ref precisely so callers can pass an arrow function
    // without it invalidating the effect on each render.
    const spy = vi.fn(async () => 'x')
    const { rerender } = render(<Probe fn={spy} deps={[]} />)
    await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('success'))

    rerender(<Probe fn={async () => 'x'} deps={[]} />)
    rerender(<Probe fn={async () => 'x'} deps={[]} />)

    expect(spy).toHaveBeenCalledTimes(1)
  })

  it('re-runs when deps change', async () => {
    const fn = vi.fn(async () => 'x')
    const { rerender } = render(<Probe fn={fn} deps={['a']} />)
    await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('success'))

    rerender(<Probe fn={fn} deps={['b']} />)
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2))
  })

  /**
   * The race that shows stale data: a slow first request finishing *after* a fast second
   * one must not overwrite it. Without run-id tracking, switching filters quickly leaves
   * the screen showing results for the filter you just left.
   */
  it('discards a superseded response', async () => {
    const resolvers: ((value: string) => void)[] = []
    const fn = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          resolvers.push(resolve)
        }),
    )

    const { rerender } = render(<Probe fn={fn} deps={['first']} />)
    rerender(<Probe fn={fn} deps={['second']} />)

    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2))

    // Resolve the *newer* request first, then the older one.
    await act(async () => {
      resolvers[1]!('second-result')
    })
    await act(async () => {
      resolvers[0]!('first-result')
    })

    expect(screen.getByTestId('data')).toHaveTextContent('second-result')
  })

  it('aborts the in-flight request on unmount', async () => {
    let captured: AbortSignal | undefined
    const { unmount } = render(
      <Probe
        fn={(signal) => {
          captured = signal
          return new Promise<string>(() => undefined) // never settles
        }}
      />,
    )

    expect(captured?.aborted).toBe(false)
    unmount()
    expect(captured?.aborted).toBe(true)
  })

  it('ignores an AbortError rather than reporting it as a failure', async () => {
    // An abort is our own doing; surfacing it would flash an error state on unmount.
    render(
      <Probe
        fn={async () => {
          throw new DOMException('aborted', 'AbortError')
        }}
      />,
    )

    // Give the rejection a chance to land.
    await new Promise((r) => setTimeout(r, 10))
    expect(screen.getByTestId('status')).toHaveTextContent('loading')
    expect(screen.getByTestId('error')).toHaveTextContent('')
  })
})

// ── useAction ─────────────────────────────────────────────────────────────────

function ActionProbe({ fn }: { fn: (value: string) => Promise<string> }) {
  const action = useAction(fn)
  return (
    <div>
      <span data-testid="pending">{String(action.isPending)}</span>
      <span data-testid="error">{action.error?.message ?? ''}</span>
      <button onClick={() => void action.run('go')}>run</button>
      <button onClick={action.reset}>reset</button>
    </div>
  )
}

describe('useAction', () => {
  it('does not run on mount', async () => {
    const fn = vi.fn(async () => 'x')
    render(<ActionProbe fn={fn} />)

    // Unlike useAsync, the trigger is an event — running on mount would submit forms by
    // themselves.
    await new Promise((r) => setTimeout(r, 10))
    expect(fn).not.toHaveBeenCalled()
  })

  it('toggles isPending around the call', async () => {
    let release: (v: string) => void = () => undefined
    render(
      <ActionProbe
        fn={() =>
          new Promise<string>((resolve) => {
            release = resolve
          })
        }
      />,
    )

    await userEvent.click(screen.getByText('run'))
    await waitFor(() => expect(screen.getByTestId('pending')).toHaveTextContent('true'))

    await act(async () => {
      release('done')
    })
    expect(screen.getByTestId('pending')).toHaveTextContent('false')
  })

  it('captures the error and returns undefined instead of throwing', async () => {
    // Callers do `await action.run()` without try/catch, so a rejection must be captured
    // rather than becoming an unhandled promise rejection.
    render(
      <ActionProbe
        fn={async () => {
          throw new ApiError(422, 'INVOICE_NOT_PAID', 'only PAID invoices can be refunded')
        }}
      />,
    )

    await userEvent.click(screen.getByText('run'))
    await waitFor(() =>
      expect(screen.getByTestId('error')).toHaveTextContent('only PAID invoices can be refunded'),
    )
    expect(screen.getByTestId('pending')).toHaveTextContent('false')
  })

  it('clears the error on reset', async () => {
    render(
      <ActionProbe
        fn={async () => {
          throw new Error('boom')
        }}
      />,
    )

    await userEvent.click(screen.getByText('run'))
    await waitFor(() => expect(screen.getByTestId('error')).toHaveTextContent('boom'))

    await userEvent.click(screen.getByText('reset'))
    expect(screen.getByTestId('error')).toHaveTextContent('')
  })

  it('does not set state after unmount', async () => {
    // A common React warning source: an action that navigates away and then rejects.
    const errors: unknown[] = []
    const spy = vi.spyOn(console, 'error').mockImplementation((...args) => errors.push(args))

    let reject: (e: Error) => void = () => undefined
    const { unmount } = render(
      <ActionProbe
        fn={() =>
          new Promise<string>((_, rej) => {
            reject = rej
          })
        }
      />,
    )

    await userEvent.click(screen.getByText('run'))
    unmount()
    await act(async () => {
      reject(new Error('late failure'))
    })

    expect(errors.filter((e) => String(e).includes('unmounted'))).toHaveLength(0)
    spy.mockRestore()
  })
})
