import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdminPaymentsPage } from './AdminPaymentsPage'
import { errorResponse, jsonResponse, paginated, renderRoute, signIn, signOut } from '@/test/render'
import type { PaymentIntent, PaymentIntentStatus } from '@/api/types'

/**
 * Payment simulation panel (SRS §4.4: find a payment intent, set SUCCESS or FAILED).
 *
 * Settling a payment credits the merchant and marks the invoice PAID, so it is
 * irreversible — the tests check that it cannot happen from a single stray click.
 */

function intent(status: PaymentIntentStatus, overrides: Partial<PaymentIntent> = {}): PaymentIntent {
  return {
    id: '11111111-1111-1111-1111-111111111111',
    invoice_id: '22222222-2222-2222-2222-222222222222',
    method: 'VA_DUMMY',
    status,
    amount: 50000,
    created_at: '2026-07-26T10:00:00Z',
    ...overrides,
  }
}

function stubApi(handler: (url: string, init: RequestInit) => Response) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) =>
    handler(String(input), init),
  )
  vi.stubGlobal('fetch', fn)
  return fn
}

function renderPage() {
  return renderRoute(<AdminPaymentsPage />, { path: '/admin/payments' })
}

const VALID_UUID = '33333333-3333-3333-3333-333333333333'

beforeEach(() => {
  signOut()
  signIn('ADMIN')
})

describe('search (SRS §4.4)', () => {
  /** The panel exists to settle pending payments, so that is the useful default. */
  it('defaults to the PENDING filter', async () => {
    const fetchMock = stubApi(() => jsonResponse(paginated([intent('PENDING')])))
    renderPage()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(String(fetchMock.mock.calls[0]![0])).toContain('status=PENDING')
  })

  it('filters by invoice id when a valid UUID is submitted', async () => {
    const fetchMock = stubApi(() => jsonResponse(paginated([intent('PENDING')])))
    renderPage()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    await userEvent.type(screen.getByLabelText(/invoice id/i), VALID_UUID)
    await userEvent.click(screen.getByRole('button', { name: /^cari$/i }))

    await waitFor(() => {
      const urls = fetchMock.mock.calls.map(([u]) => String(u))
      expect(urls.some((u) => u.includes(`invoice_id=${VALID_UUID}`))).toBe(true)
    })
  })

  /**
   * The filter is applied on submit, not per keystroke. A half-typed UUID would otherwise
   * fire a request per character and every one would come back 400.
   */
  it('rejects a malformed UUID without querying', async () => {
    const fetchMock = stubApi(() => jsonResponse(paginated([intent('PENDING')])))
    renderPage()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const callsBefore = fetchMock.mock.calls.length

    await userEvent.type(screen.getByLabelText(/invoice id/i), 'not-a-uuid')
    await userEvent.click(screen.getByRole('button', { name: /^cari$/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/uuid/i)
    expect(fetchMock.mock.calls).toHaveLength(callsBefore)
  })

  it('does not send an empty status filter when set to all', async () => {
    const fetchMock = stubApi(() => jsonResponse(paginated([])))
    renderPage()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    await userEvent.selectOptions(screen.getByLabelText(/status/i), '')

    await waitFor(() => {
      const last = String(fetchMock.mock.calls.at(-1)![0])
      // `status=` with no value would make the backend decide what an empty filter means.
      expect(last).not.toContain('status=')
    })
  })

  it('resets the filters', async () => {
    const fetchMock = stubApi(() => jsonResponse(paginated([])))
    renderPage()

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    await userEvent.type(screen.getByLabelText(/invoice id/i), VALID_UUID)
    await userEvent.click(screen.getByRole('button', { name: /reset filter/i }))

    expect(screen.getByLabelText(/invoice id/i)).toHaveValue('')
  })
})

describe('settling a payment', () => {
  it('requires confirmation before sending SUCCESS', async () => {
    const fetchMock = stubApi((_url, init) => {
      if (init.method === 'PATCH') return jsonResponse(intent('SUCCESS'))
      return jsonResponse(paginated([intent('PENDING')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /^SUCCESS$/ })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^SUCCESS$/ }))

    // Nothing sent yet — crediting a merchant cannot be undone.
    expect(fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'PATCH')).toHaveLength(0)

    await userEvent.click(screen.getByRole('button', { name: /ya, sukseskan/i }))

    await waitFor(() => {
      const patches = fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'PATCH')
      expect(patches).toHaveLength(1)
      expect(JSON.parse(String((patches[0]![1] as RequestInit).body))).toEqual({ action: 'SUCCESS' })
    })
  })

  it('sends FAILED for the failure action', async () => {
    const fetchMock = stubApi((_url, init) => {
      if (init.method === 'PATCH') return jsonResponse(intent('FAILED'))
      return jsonResponse(paginated([intent('PENDING')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /^FAILED$/ })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^FAILED$/ }))
    await userEvent.click(screen.getByRole('button', { name: /ya, gagalkan/i }))

    await waitFor(() => {
      const patch = fetchMock.mock.calls.find(([, i]) => (i as RequestInit)?.method === 'PATCH')
      expect(JSON.parse(String((patch![1] as RequestInit).body))).toEqual({ action: 'FAILED' })
    })
  })

  it('sends nothing when the confirmation is cancelled', async () => {
    const fetchMock = stubApi((_url, init) => {
      if (init.method === 'PATCH') return jsonResponse(intent('SUCCESS'))
      return jsonResponse(paginated([intent('PENDING')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /^SUCCESS$/ })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^SUCCESS$/ }))
    await userEvent.click(screen.getByRole('button', { name: /batal/i }))

    expect(fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'PATCH')).toHaveLength(0)
    expect(screen.getByRole('button', { name: /^SUCCESS$/ })).toBeInTheDocument()
  })

  /** PENDING → SUCCESS | FAILED are the only edges; a settled intent is terminal. */
  it('offers no action for an already-settled intent', async () => {
    stubApi(() => jsonResponse(paginated([intent('SUCCESS')])))
    renderPage()

    await waitFor(() => expect(screen.getByText(/sudah final/i)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /^SUCCESS$/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^FAILED$/ })).not.toBeInTheDocument()
  })

  it('shows the server error next to the row it belongs to', async () => {
    stubApi((_url, init) => {
      if (init.method === 'PATCH') {
        return errorResponse(422, 'INVALID_STATE', 'invalid state transition')
      }
      return jsonResponse(paginated([intent('PENDING')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /^SUCCESS$/ })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^SUCCESS$/ }))
    await userEvent.click(screen.getByRole('button', { name: /ya, sukseskan/i }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(/invalid state transition/i),
    )
  })
})

describe('list states (SRS §4.1)', () => {
  it('shows an empty state', async () => {
    stubApi(() => jsonResponse(paginated([])))
    renderPage()

    await waitFor(() => expect(screen.getByText(/tidak ada payment intent/i)).toBeInTheDocument())
  })

  it('shows an error state with retry', async () => {
    stubApi(() => errorResponse(500, 'INTERNAL', 'internal server error'))
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /coba lagi/i })).toBeInTheDocument())
  })

  it('renders the intent details', async () => {
    stubApi(() => jsonResponse(paginated([intent('PENDING', { amount: 75000, method: 'WALLET' })])))
    renderPage()

    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    expect(screen.getByText(/75\.000/)).toBeInTheDocument()
    expect(screen.getByText('Saldo Wallet')).toBeInTheDocument()
  })
})
