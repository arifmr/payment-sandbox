import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PaymentPage } from './PaymentPage'
import { errorResponse, jsonResponse, renderRoute, signOut } from '@/test/render'
import { getPendingIntent, setPendingIntent } from '@/lib/pendingIntent'
import type { PaymentIntent, PublicInvoice } from '@/api/types'

/**
 * Public payment page (SRS §2.4 / §4.3).
 *
 * The customer's whole journey lives here, so the tests cover it end to end: see the
 * invoice, pick a method, create the intent, watch the status settle.
 */

const invoice: PublicInvoice = {
  invoice_number: 'INV-20260726-A1B2C3D4E5',
  merchant_name: 'Toko A',
  customer_name: 'Budi',
  description: 'Pembayaran pesanan #1',
  amount: 50000,
  status: 'PENDING',
  due_date: '2026-08-01T23:59:59Z',
}

const pendingIntent: PaymentIntent = {
  id: '22222222-2222-2222-2222-222222222222',
  invoice_id: '33333333-3333-3333-3333-333333333333',
  method: 'VA_DUMMY',
  status: 'PENDING',
  amount: 50000,
  created_at: '2026-07-26T10:00:00Z',
}

function renderPage() {
  return renderRoute(<PaymentPage />, { path: '/pay/tok-abc', route: '/pay/:token' })
}

/** Routes fetch by URL and method so a test only declares what it cares about. */
function stubApi(handler: (url: string, init: RequestInit) => Response) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) =>
    handler(String(input), init),
  )
  vi.stubGlobal('fetch', fn)
  return fn
}

/** Must match POLL_INTERVAL_MS in PaymentPage.tsx. */
const POLL_MS = 3000

/**
 * Lets pending promises settle and React flush, under either real or fake timers.
 * Under fake timers `await Promise.resolve()` alone is not enough — microtasks queued by
 * fetch handlers need a turn of the event loop that the fake clock does not provide.
 */
async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

beforeEach(signOut)

describe('loading and error states (SRS §4.1)', () => {
  it('shows a loading state before the invoice arrives', async () => {
    stubApi(() => jsonResponse(invoice))
    renderPage()

    // The very first paint is already 'loading', so there is never a frame with no state.
    expect(screen.getByRole('status')).toHaveTextContent(/memuat/i)

    // Let the request settle inside the test. Without this the state update lands after
    // the test has finished, which React reports as an update outside act() — a warning
    // that is easy to dismiss but genuinely means the assertion raced the render.
    await waitFor(() => expect(screen.getByText('Toko A')).toBeInTheDocument())
  })

  it('shows an error state with a retry when the invoice cannot be loaded', async () => {
    stubApi(() => errorResponse(404, 'NOT_FOUND', 'resource not found'))
    renderPage()

    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /coba lagi/i })).toBeInTheDocument()
  })

  it('retries when asked', async () => {
    let attempts = 0
    stubApi(() => {
      attempts += 1
      return attempts === 1
        ? errorResponse(500, 'INTERNAL', 'internal server error')
        : jsonResponse(invoice)
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /coba lagi/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /coba lagi/i }))

    await waitFor(() => expect(screen.getByText('Toko A')).toBeInTheDocument())
  })

  /** A 500's message is deliberately generic on the backend; it must not be shown raw. */
  it('shows a generic message for a server fault', async () => {
    stubApi(() => errorResponse(500, 'INTERNAL', 'internal server error'))
    renderPage()

    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByRole('alert')).toHaveTextContent(/server sedang bermasalah/i)
  })
})

describe('invoice summary', () => {
  it('shows the details a payer needs', async () => {
    stubApi(() => jsonResponse(invoice))
    renderPage()

    await waitFor(() => expect(screen.getByText('Toko A')).toBeInTheDocument())
    expect(screen.getByText('INV-20260726-A1B2C3D4E5')).toBeInTheDocument()
    expect(screen.getByText('Budi')).toBeInTheDocument()
    expect(screen.getByText('Pembayaran pesanan #1')).toBeInTheDocument()
    // The amount appears twice by design — in the summary and on the pay button — so the
    // assertion is scoped to the summary block rather than matching the page loosely.
    const totalLabel = screen.getByText(/total tagihan/i)
    expect(totalLabel.parentElement).toHaveTextContent(/50\.000/)
    expect(screen.getByRole('button', { name: /bayar/i })).toHaveTextContent(/50\.000/)
  })

  it('needs no authentication', async () => {
    const fetchMock = stubApi(() => jsonResponse(invoice))
    renderPage()

    await waitFor(() => expect(screen.getByText('Toko A')).toBeInTheDocument())
    const headers = (fetchMock.mock.calls[0]?.[1]?.headers ?? {}) as Record<string, string>
    expect(headers['authorization']).toBeUndefined()
  })
})

describe('method selection (SRS §2.4)', () => {
  it('offers exactly the three supported methods', async () => {
    stubApi(() => jsonResponse(invoice))
    renderPage()

    await waitFor(() => expect(screen.getByRole('radiogroup')).toBeInTheDocument())
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(3)
    expect(screen.getByText('Virtual Account')).toBeInTheDocument()
    expect(screen.getByText('E-Wallet')).toBeInTheDocument()
    expect(screen.getByText('Saldo Wallet')).toBeInTheDocument()
  })

  it('sends the selected method when creating the intent', async () => {
    const fetchMock = stubApi((_url, init) => {
      if (init.method === 'POST') return jsonResponse({ ...pendingIntent, method: 'WALLET' })
      return jsonResponse(invoice)
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('radiogroup')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('radio', { name: /saldo wallet/i }))
    await userEvent.click(screen.getByRole('button', { name: /bayar/i }))

    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === 'POST')
    expect(JSON.parse(String((post?.[1] as RequestInit).body))).toEqual({ method: 'WALLET' })
  })

  it('surfaces a rejection from the server', async () => {
    stubApi((_url, init) => {
      if (init.method === 'POST') {
        return errorResponse(422, 'INVOICE_EXPIRED', 'invoice has expired')
      }
      return jsonResponse(invoice)
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /bayar/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /bayar/i }))

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/invoice has expired/i))
  })

  /**
   * A settled or expired invoice cannot be paid. The backend rejects it too, but showing a
   * form that is guaranteed to fail wastes the customer's time.
   */
  it('hides the form for an already-paid invoice', async () => {
    stubApi(() => jsonResponse({ ...invoice, status: 'PAID' }))
    renderPage()

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(/sudah dibayar/i))
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })

  it('hides the form for an expired invoice', async () => {
    stubApi(() => jsonResponse({ ...invoice, status: 'EXPIRED' }))
    renderPage()

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(/kedaluwarsa/i))
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })
})

describe('payment status (SRS §4.3)', () => {
  it('shows the pending state after creating an intent', async () => {
    stubApi((_url, init) => {
      if (init.method === 'POST') return jsonResponse(pendingIntent)
      return jsonResponse(invoice)
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /bayar/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /bayar/i }))

    await waitFor(() => expect(screen.getByText(/status pembayaran/i)).toBeInTheDocument())
    expect(screen.getByText(/sedang diverifikasi/i)).toBeInTheDocument()
    // The payment id is the customer's reference if they need to ask about it later.
    expect(screen.getByText(pendingIntent.id)).toBeInTheDocument()
  })

  /**
   * A PENDING intent is settled by an admin out of band, so the page polls. Polling must
   * stop once the intent is terminal, or a tab left open hammers the API forever.
   *
   * Driven with fireEvent rather than userEvent: userEvent installs its own timer bridging,
   * which fights vi.advanceTimersByTimeAsync and makes the test hang instead of fail.
   */
  it('polls until the intent settles, then stops', async () => {
    vi.useFakeTimers()
    let pollCount = 0
    stubApi((url, init) => {
      if (init.method === 'POST') return jsonResponse(pendingIntent)
      if (url.includes('/intents/')) {
        pollCount += 1
        return jsonResponse({
          ...pendingIntent,
          // Settle on the second poll.
          status: pollCount >= 2 ? 'SUCCESS' : 'PENDING',
          ...(pollCount >= 2 ? { processed_at: '2026-07-26T10:05:00Z' } : {}),
        })
      }
      return jsonResponse(invoice)
    })

    renderPage()
    await flush()

    fireEvent.click(screen.getByRole('button', { name: /bayar/i }))
    await flush()
    expect(screen.getByText(/sedang diverifikasi/i)).toBeInTheDocument()

    // Two intervals is enough to reach SUCCESS.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_MS * 2 + 100)
    })
    expect(screen.getByText(/pembayaran berhasil/i)).toBeInTheDocument()

    const countAtSettle = pollCount
    // Several further intervals: a terminal intent must not be polled again.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_MS * 5)
    })
    expect(pollCount).toBe(countAtSettle)

    vi.useRealTimers()
  })

  it('offers another attempt after a failed payment', async () => {
    vi.useFakeTimers()
    stubApi((url, init) => {
      if (init.method === 'POST') return jsonResponse(pendingIntent)
      if (url.includes('/intents/')) {
        return jsonResponse({
          ...pendingIntent,
          status: 'FAILED',
          processed_at: '2026-07-26T10:05:00Z',
        })
      }
      return jsonResponse(invoice)
    })

    renderPage()
    await flush()
    fireEvent.click(screen.getByRole('button', { name: /bayar/i }))
    await flush()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_MS + 100)
    })

    expect(screen.getByText(/pembayaran gagal/i)).toBeInTheDocument()
    // FAILED is terminal on the backend, so recovery means creating a *new* intent.
    expect(screen.getByRole('button', { name: /metode pembayaran lain/i })).toBeInTheDocument()

    vi.useRealTimers()
  })

  /**
   * Starting over must forget the failed intent, not just show the picker once. Otherwise
   * a refresh right after clicking "try another method" would restore the dead FAILED
   * intent instead of letting the payer's new choice take effect.
   */
  it('forgets the failed intent once the payer starts over', async () => {
    vi.useFakeTimers()
    stubApi((url, init) => {
      if (init.method === 'POST') return jsonResponse(pendingIntent)
      if (url.includes('/intents/')) {
        return jsonResponse({ ...pendingIntent, status: 'FAILED', processed_at: '2026-07-26T10:05:00Z' })
      }
      return jsonResponse(invoice)
    })

    renderPage()
    await flush()
    fireEvent.click(screen.getByRole('button', { name: /bayar/i }))
    await flush()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_MS + 100)
    })

    expect(getPendingIntent('tok-abc')).toBe(pendingIntent.id)

    fireEvent.click(screen.getByRole('button', { name: /metode pembayaran lain/i }))
    await flush()

    expect(screen.getByRole('radiogroup')).toBeInTheDocument()
    expect(getPendingIntent('tok-abc')).toBeNull()

    vi.useRealTimers()
  })

  it('survives a transient polling failure instead of giving up', async () => {
    vi.useFakeTimers()
    let polls = 0
    stubApi((url, init) => {
      if (init.method === 'POST') return jsonResponse(pendingIntent)
      if (url.includes('/intents/')) {
        polls += 1
        // The first poll errors; the next must still run rather than the loop dying.
        if (polls === 1) return errorResponse(500, 'INTERNAL', 'internal server error')
        return jsonResponse({ ...pendingIntent, status: 'SUCCESS' })
      }
      return jsonResponse(invoice)
    })

    renderPage()
    await flush()
    fireEvent.click(screen.getByRole('button', { name: /bayar/i }))
    await flush()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_MS * 3)
    })

    expect(screen.getByText(/pembayaran berhasil/i)).toBeInTheDocument()
    expect(polls).toBeGreaterThan(1)

    vi.useRealTimers()
  })
})

/**
 * The bug report this section exists for: a payer picks a method, clicks Bayar, then
 * refreshes the tab while an admin has not settled it yet — and used to land back on the
 * method picker, inviting a second payment for something already PENDING. `intent` alone
 * lived in React state, so any remount lost it. See lib/pendingIntent.ts.
 *
 * `renderPage().unmount()` followed by a fresh `renderPage()` is the closest a jsdom test
 * gets to an actual browser refresh: a full component teardown and remount, with
 * localStorage — unlike React state — left untouched in between.
 */
describe('resuming after a refresh (SRS §4.3)', () => {
  it('shows payment status instead of the method picker again, without creating a second intent', async () => {
    let createCalls = 0
    stubApi((url, init) => {
      if (init.method === 'POST') {
        createCalls += 1
        return jsonResponse(pendingIntent)
      }
      if (url.includes('/intents/')) return jsonResponse(pendingIntent)
      return jsonResponse(invoice)
    })

    const firstMount = renderPage()
    await waitFor(() => expect(screen.getByRole('button', { name: /bayar/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /bayar/i }))
    await waitFor(() => expect(screen.getByText(/status pembayaran/i)).toBeInTheDocument())
    expect(createCalls).toBe(1)

    firstMount.unmount()
    renderPage()

    await waitFor(() => expect(screen.getByText(/status pembayaran/i)).toBeInTheDocument())
    expect(screen.getByText(/sedang diverifikasi/i)).toBeInTheDocument()
    expect(screen.getByText(pendingIntent.id)).toBeInTheDocument()
    // The whole point: a refresh must not be a second attempt at paying.
    expect(createCalls).toBe(1)
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })

  it('shows the final state if the intent had already settled while the tab was away', async () => {
    stubApi((url, init) => {
      if (init.method === 'POST') return jsonResponse(pendingIntent)
      if (url.includes('/intents/')) return jsonResponse({ ...pendingIntent, status: 'SUCCESS' })
      return jsonResponse(invoice)
    })

    const firstMount = renderPage()
    await waitFor(() => expect(screen.getByRole('button', { name: /bayar/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /bayar/i }))
    await waitFor(() => expect(screen.getByText(/status pembayaran/i)).toBeInTheDocument())

    firstMount.unmount()
    renderPage()

    await waitFor(() => expect(screen.getByText(/pembayaran berhasil/i)).toBeInTheDocument())
  })

  it('falls back to the method picker and forgets the id if it no longer resolves', async () => {
    setPendingIntent('tok-abc', 'stale-intent-id')
    stubApi((url) => {
      if (url.includes('/intents/')) return errorResponse(404, 'NOT_FOUND', 'resource not found')
      return jsonResponse(invoice)
    })

    renderPage()

    await waitFor(() => expect(screen.getByRole('radiogroup')).toBeInTheDocument())
    // A stale id must not be retried forever on every future visit.
    expect(getPendingIntent('tok-abc')).toBeNull()
  })

  it('does not restore an intent stored under a different payment link', async () => {
    setPendingIntent('some-other-token', 'unrelated-intent-id')
    let intentsCalled = false
    stubApi((url) => {
      if (url.includes('/intents/')) intentsCalled = true
      return jsonResponse(invoice)
    })

    renderPage() // this route's token is 'tok-abc'

    await waitFor(() => expect(screen.getByRole('radiogroup')).toBeInTheDocument())
    expect(intentsCalled).toBe(false)
  })
})
