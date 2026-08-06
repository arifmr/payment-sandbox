import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InvoiceDetailPage } from './InvoiceDetailPage'
import { errorResponse, jsonResponse, paginated, renderRoute, signIn, signOut } from '@/test/render'
import type { Invoice, Refund, RefundStatus } from '@/api/types'

/**
 * Invoice detail — payment link and refund request/status (SRS §2.5 / §4.2).
 *
 * The refund section is the point of this file. Two bugs are fixed here, both reported
 * directly by a user testing the app:
 *
 *  1. The request form kept showing even with a refund already REQUESTED/APPROVED for the
 *     same invoice, inviting a second submission for something already pending.
 *  2. Whether a refund had been submitted lived only in this component's local state, so a
 *     reload of the page (the very next thing someone does after submitting, to check on
 *     it) forgot that and showed the form again.
 *
 * The fix derives "is there already an active refund" from the merchant's own refund list
 * (GET /refunds) rather than from anything local, so these tests deliberately exercise a
 * *reload* — unmounting and rendering fresh — the same way PaymentPage.test.tsx proves its
 * equivalent fix for the payment page.
 */

const paidInvoice: Invoice = {
  id: '11111111-1111-1111-1111-111111111111',
  invoice_number: 'INV-20260726-A1B2C3D4E5',
  merchant_id: '99999999-9999-9999-9999-999999999999',
  customer_name: 'Budi',
  customer_email: 'budi@example.com',
  description: 'Pesanan #1',
  amount: 100_000,
  status: 'PAID',
  due_date: '2026-08-01T23:59:59Z',
  payment_token: 'tok-abc',
  created_at: '2026-07-20T10:00:00Z',
  paid_at: '2026-07-21T10:00:00Z',
}

const pendingInvoice: Invoice = { ...paidInvoice, status: 'PENDING', paid_at: undefined }

function refund(status: RefundStatus, overrides: Partial<Refund> = {}): Refund {
  return {
    id: '22222222-2222-2222-2222-222222222222',
    invoice_id: paidInvoice.id,
    payment_intent_id: '33333333-3333-3333-3333-333333333333',
    merchant_id: paidInvoice.merchant_id,
    amount: 40_000,
    reason: 'Pelanggan membatalkan pesanan',
    status,
    created_at: '2026-07-22T10:00:00Z',
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
  return renderRoute(<InvoiceDetailPage />, {
    path: `/merchant/invoices/${paidInvoice.id}`,
    route: '/merchant/invoices/:id',
  })
}

/** Routes GET /invoices/:id vs GET /refunds vs POST /refunds by method and path. */
function stubWith(opts: {
  invoice?: Invoice
  refunds?: Refund[]
  onCreate?: (body: unknown) => Response
}) {
  const invoice = opts.invoice ?? paidInvoice
  const items = opts.refunds ?? []
  return stubApi((url, init) => {
    if (init.method === 'POST' && url.includes('/refunds')) {
      const body: unknown = init.body ? JSON.parse(String(init.body)) : {}
      return opts.onCreate ? opts.onCreate(body) : jsonResponse(refund('REQUESTED'))
    }
    if (url.includes('/refunds')) return jsonResponse(paginated(items))
    return jsonResponse(invoice)
  })
}

beforeEach(() => {
  signOut()
  signIn('MERCHANT', { id: paidInvoice.merchant_id })
})

describe('refund form visibility', () => {
  it('does not offer a refund for an unpaid invoice', async () => {
    stubWith({ invoice: pendingInvoice })
    renderPage()

    await waitFor(() => expect(screen.getByText(paidInvoice.invoice_number)).toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: /^refund$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /ajukan refund/i })).not.toBeInTheDocument()
  })

  it('shows the request form for a paid invoice with no refund history', async () => {
    stubWith({ refunds: [] })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
  })

  it('hides the form and shows status instead when a refund is REQUESTED', async () => {
    stubWith({ refunds: [refund('REQUESTED')] })
    renderPage()

    await waitFor(() => expect(screen.getByText(/menunggu persetujuan admin/i)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /ajukan refund/i })).not.toBeInTheDocument()
  })

  it('hides the form and shows status instead when a refund is APPROVED', async () => {
    stubWith({ refunds: [refund('APPROVED')] })
    renderPage()

    await waitFor(() => expect(screen.getByText(/menunggu diproses admin/i)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /ajukan refund/i })).not.toBeInTheDocument()
  })

  it('brings the form back once the only refund is REJECTED', async () => {
    stubWith({ refunds: [refund('REJECTED')] })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
  })

  it('brings the form back once the only refund is FAILED', async () => {
    stubWith({ refunds: [refund('FAILED')] })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
  })

  it('brings the form back once the only refund reached SUCCESS', async () => {
    stubWith({ refunds: [refund('SUCCESS')] })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
  })

  it('only reacts to refunds belonging to this invoice, not another one', async () => {
    stubWith({ refunds: [refund('REQUESTED', { invoice_id: 'some-other-invoice' })] })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
  })
})

describe('refund history ("keterangan refund")', () => {
  it('shows amount, status and reason for a past refund', async () => {
    stubWith({ refunds: [refund('REJECTED', { amount: 25_000, reason: 'Barang rusak' })] })
    renderPage()

    await waitFor(() => expect(screen.getByText('Barang rusak')).toBeInTheDocument())
    expect(screen.getByText(/25\.000/)).toBeInTheDocument()
  })

  it('lists more than one past refund for the invoice', async () => {
    stubWith({
      refunds: [
        refund('REJECTED', { id: 'r1', amount: 10_000, reason: 'Percobaan pertama' }),
        refund('SUCCESS', { id: 'r2', amount: 20_000, reason: 'Percobaan kedua', processed_at: '2026-07-23T00:00:00Z' }),
      ],
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('Percobaan pertama')).toBeInTheDocument())
    expect(screen.getByText('Percobaan kedua')).toBeInTheDocument()
  })
})

describe('submitting a refund', () => {
  /**
   * This is the second reported bug, reproduced directly: right after a successful
   * submission, the form must already be gone — before any manual reload of the page.
   *
   * Submitting triggers RefundSection's own reload of GET /refunds (`onChanged` calls
   * `list.reload()`), so the stub has to hand back the newly created refund on that next
   * GET — a static list would not reproduce this, which is why this test tracks it in a
   * closure instead of using the `stubWith` helper.
   */
  it('hides the form immediately after a successful submission, without a page reload', async () => {
    let stored: Refund | null = null
    stubApi((url, init) => {
      if (init.method === 'POST' && url.includes('/refunds')) {
        stored = refund('REQUESTED', { amount: 100_000 })
        return jsonResponse(stored)
      }
      if (url.includes('/refunds')) return jsonResponse(paginated(stored ? [stored] : []))
      return jsonResponse(paidInvoice)
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /ajukan refund/i }))

    await waitFor(() => expect(screen.getByText(/menunggu persetujuan admin/i)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /ajukan refund/i })).not.toBeInTheDocument()
  })

  /**
   * The first reported bug, as a reload: submit, then remount the page (the closest a
   * jsdom test gets to a browser refresh) and confirm the form does not come back. Local
   * component state would not survive this; the server-derived list does.
   */
  it('stays hidden after a refresh, because the state comes from the server, not local memory', async () => {
    let stored: Refund | null = null
    stubApi((url, init) => {
      if (init.method === 'POST' && url.includes('/refunds')) {
        stored = refund('REQUESTED', { amount: 100_000 })
        return jsonResponse(stored)
      }
      if (url.includes('/refunds')) return jsonResponse(paginated(stored ? [stored] : []))
      return jsonResponse(paidInvoice)
    })

    const firstMount = renderPage()
    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /ajukan refund/i }))
    await waitFor(() => expect(screen.getByText(/menunggu persetujuan admin/i)).toBeInTheDocument())

    firstMount.unmount()
    renderPage()

    await waitFor(() => expect(screen.getByText(/menunggu persetujuan admin/i)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /ajukan refund/i })).not.toBeInTheDocument()
  })

  it('surfaces the backend rejection when the cumulative cap is exceeded', async () => {
    stubWith({
      refunds: [],
      onCreate: () => errorResponse(422, 'REFUND_EXCEEDS_INVOICE', 'refund exceeds invoice amount'),
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /ajukan refund/i }))

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/refund exceeds invoice amount/i))
    // The rejection must not be mistaken for a hidden success: the form stays put so the
    // merchant can adjust the amount and retry.
    expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument()
  })

  it('validates the amount client-side before calling the server', async () => {
    const fetchMock = stubWith({ refunds: [] })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /ajukan refund/i })).toBeInTheDocument())
    const callsBefore = fetchMock.mock.calls.length

    await userEvent.clear(screen.getByLabelText(/nominal refund/i))
    await userEvent.type(screen.getByLabelText(/nominal refund/i), '0')
    await userEvent.click(screen.getByRole('button', { name: /ajukan refund/i }))

    expect(await screen.findByText(/lebih besar dari 0/i)).toBeInTheDocument()
    expect(fetchMock.mock.calls.length).toBe(callsBefore)
  })
})
