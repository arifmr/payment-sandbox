import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route } from 'react-router-dom'
import { InvoiceCreatePage } from './InvoiceCreatePage'
import { errorResponse, jsonResponse, renderRoute, signIn, signOut } from '@/test/render'
import { todayISODate } from '@/lib/format'
import type { Invoice } from '@/api/types'

/**
 * Create-invoice form. This is where SRS §4.5 lands: amount > 0, date ≥ today, valid email,
 * clear messages.
 */

const created: Invoice = {
  id: '55555555-5555-5555-5555-555555555555',
  invoice_number: 'INV-20260726-A1B2C3D4E5',
  merchant_id: '11111111-1111-1111-1111-111111111111',
  customer_name: 'Budi',
  customer_email: '',
  description: '',
  amount: 50000,
  status: 'PENDING',
  due_date: '2026-08-01T23:59:59Z',
  payment_token: 'tok-abc',
  created_at: '2026-07-26T10:00:00Z',
}

function stubApi(handler: (url: string, init: RequestInit) => Response) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) =>
    handler(String(input), init),
  )
  vi.stubGlobal('fetch', fn)
  return fn
}

function renderPage() {
  return renderRoute(<InvoiceCreatePage />, {
    path: '/merchant/invoices/new',
    extraRoutes: (
      <>
        <Route path="/merchant/invoices/:id" element={<p>invoice detail</p>} />
        <Route path="/merchant/invoices" element={<p>invoice list</p>} />
      </>
    ),
  })
}

/** Fills only the required fields, leaving the pre-filled due date alone. */
async function fillRequired(amount = '50000') {
  await userEvent.type(screen.getByLabelText(/nama pelanggan/i), 'Budi')
  const amountField = screen.getByLabelText(/nominal/i)
  await userEvent.clear(amountField)
  await userEvent.type(amountField, amount)
}

beforeEach(() => {
  signOut()
  signIn('MERCHANT')
})

describe('amount validation (SRS §4.5: amount > 0)', () => {
  it('rejects zero', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired('0')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText(/lebih besar dari 0/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('rejects a negative amount', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired('-100')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText(/lebih besar dari 0/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  /** The API takes whole rupiah; silently rounding would change what the user asked for. */
  it('rejects a fractional amount', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired('100.5')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText(/bilangan bulat/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('accepts the smallest valid amount', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired('1')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  })
})

describe('due date validation (SRS §4.5: date ≥ today)', () => {
  /**
   * The picker's own `min` blocks past dates in the browser UI, but attributes can be
   * bypassed, so the value is validated again on submit.
   */
  it('constrains the picker to today or later', () => {
    stubApi(() => jsonResponse(created, 201))
    renderPage()

    expect(screen.getByLabelText(/jatuh tempo/i)).toHaveAttribute('min', todayISODate())
  })

  it('rejects a date in the past', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired()
    const due = screen.getByLabelText(/jatuh tempo/i)
    await userEvent.clear(due)
    await userEvent.type(due, '2020-01-01')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText(/tidak boleh di masa lalu/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  /**
   * Today must be accepted. The chosen day is converted to its final second, because
   * midnight has already passed and the backend rejects a past due date — a naive
   * conversion makes same-day invoices impossible.
   */
  it('accepts today and sends the end of that day', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired()
    const due = screen.getByLabelText(/jatuh tempo/i)
    await userEvent.clear(due)
    await userEvent.type(due, todayISODate())
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const body = JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))
    expect(new Date(body.due_date).getTime()).toBeGreaterThan(Date.now())
  })

  it('defaults to a date that is already valid', () => {
    stubApi(() => jsonResponse(created, 201))
    renderPage()

    // A form that starts in an invalid state trains users to ignore errors.
    const value = (screen.getByLabelText(/jatuh tempo/i) as HTMLInputElement).value
    expect(value >= todayISODate()).toBe(true)
  })
})

describe('other fields', () => {
  it('requires a customer name', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    const amountField = screen.getByLabelText(/nominal/i)
    await userEvent.clear(amountField)
    await userEvent.type(amountField, '50000')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText(/nama pelanggan wajib diisi/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('treats the customer email as optional but validates it when given', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired()
    await userEvent.type(screen.getByLabelText(/email pelanggan/i), 'not-an-email')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText(/format email tidak valid/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('omits empty optional fields from the request', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired()
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const body = JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))
    // Sending '' would fail the backend's `omitempty,email` rule on customer_email.
    expect(body).not.toHaveProperty('customer_email')
    expect(body).not.toHaveProperty('description')
    expect(body.customer_name).toBe('Budi')
    expect(body.amount).toBe(50000)
  })

  it('sends the amount as a number, not a string', async () => {
    const fetchMock = stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired('12345')
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const body = JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))
    // The DTO field is int64; a string fails binding with a confusing message.
    expect(typeof body.amount).toBe('number')
  })
})

describe('after submitting', () => {
  /** The payment link is the point of creating an invoice, and it lives on the detail page. */
  it('navigates to the new invoice detail page', async () => {
    stubApi(() => jsonResponse(created, 201))
    renderPage()

    await fillRequired()
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByText('invoice detail')).toBeInTheDocument()
  })

  it('shows a server rejection without losing the entered values', async () => {
    stubApi(() => errorResponse(400, 'INVALID_DUE_DATE', 'due_date must be in the future'))
    renderPage()

    await fillRequired()
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/due_date must be in the future/i)
    // Clearing the form on failure would force the user to retype everything.
    expect(screen.getByLabelText(/nama pelanggan/i)).toHaveValue('Budi')
  })

  /** Creating an invoice twice by double-clicking would be a real nuisance to undo. */
  it('disables the submit button while the request is in flight', async () => {
    let release: (r: Response) => void = () => undefined
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            release = resolve
          }),
      ),
    )
    renderPage()

    await fillRequired()
    await userEvent.click(screen.getByRole('button', { name: /buat invoice/i }))

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /buat invoice/i })).toBeDisabled(),
    )

    // Let the request settle inside the test. Releasing it without awaiting the resulting
    // navigation leaves a state update landing after the test ends, which React reports as
    // an update outside act() — a warning that really means the test raced the render.
    release(jsonResponse(created, 201))
    expect(await screen.findByText('invoice detail')).toBeInTheDocument()
  })

  it('goes back to the list when cancelled', async () => {
    stubApi(() => jsonResponse(created, 201))
    renderPage()

    await userEvent.click(screen.getByRole('button', { name: /batal/i }))
    expect(await screen.findByText('invoice list')).toBeInTheDocument()
  })
})
