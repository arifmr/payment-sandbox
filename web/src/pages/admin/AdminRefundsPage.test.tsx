import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdminRefundsPage } from './AdminRefundsPage'
import { errorResponse, jsonResponse, paginated, renderRoute, signIn, signOut } from '@/test/render'
import type { Refund, RefundStatus } from '@/api/types'

/**
 * Refund management (SRS §4.4).
 *
 * The point of these tests is that the UI derives its available actions from the refund's
 * status, mirroring the backend's RefundFSM. If the two drift, the UI starts offering
 * transitions the server answers with 422 INVALID_STATE — the failure is invisible until a
 * user clicks it.
 */

function refund(status: RefundStatus, overrides: Partial<Refund> = {}): Refund {
  return {
    id: '11111111-1111-1111-1111-111111111111',
    invoice_id: '22222222-2222-2222-2222-222222222222',
    payment_intent_id: '33333333-3333-3333-3333-333333333333',
    merchant_id: '44444444-4444-4444-4444-444444444444',
    amount: 1500,
    reason: 'Pelanggan membatalkan',
    status,
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
  return renderRoute(<AdminRefundsPage />, { path: '/admin/refunds' })
}

beforeEach(() => {
  signOut()
  signIn('ADMIN')
})

describe('actions derived from status (mirrors constant.RefundFSM)', () => {
  /**
   * REQUESTED → APPROVED | REJECTED. Crucially, PROCESS must NOT be offered: the backend
   * forbids REQUESTED → SUCCESS, because approval is what gates a payout.
   */
  it('offers only approve and reject for REQUESTED', async () => {
    stubApi(() => jsonResponse(paginated([refund('REQUESTED')])))
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /setujui/i })).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /^tolak$/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /cairkan/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /tandai gagal/i })).not.toBeInTheDocument()
  })

  /** APPROVED → SUCCESS | FAILED. Approve/reject are spent. */
  it('offers only process and fail for APPROVED', async () => {
    stubApi(() => jsonResponse(paginated([refund('APPROVED')])))
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /cairkan/i })).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /tandai gagal/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /setujui/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^tolak$/i })).not.toBeInTheDocument()
  })

  /** REJECTED, SUCCESS and FAILED are terminal — no edge leaves them. */
  it('offers nothing for a terminal status', async () => {
    // Rendered one at a time so each assertion is unambiguous about which row it refers to.
    for (const status of ['REJECTED', 'SUCCESS', 'FAILED'] as const) {
      stubApi(() => jsonResponse(paginated([refund(status)])))
      const view = renderPage()

      await waitFor(() => expect(screen.getByText(/sudah final/i)).toBeInTheDocument())
      expect(
        screen.queryByRole('button', { name: /setujui|cairkan|^tolak$|tandai gagal/i }),
        status,
      ).toBeNull()

      view.unmount()
    }
  })
})

describe('confirmation before acting', () => {
  /**
   * PROCESS debits the merchant's wallet and cannot be undone, so it must not fire from a
   * single click in a table row.
   */
  it('requires confirmation and only then calls the API', async () => {
    const fetchMock = stubApi((_url, init) => {
      if (init.method === 'PATCH') return jsonResponse(refund('SUCCESS'))
      return jsonResponse(paginated([refund('APPROVED')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /cairkan/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /cairkan/i }))

    // Nothing has been sent yet.
    expect(fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'PATCH')).toHaveLength(0)
    // And the consequence is spelled out.
    expect(screen.getByText(/saldo merchant akan dipotong/i)).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /konfirmasi/i }))

    await waitFor(() => {
      const patches = fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'PATCH')
      expect(patches).toHaveLength(1)
      expect(JSON.parse(String((patches[0]![1] as RequestInit).body))).toEqual({ action: 'PROCESS' })
    })
  })

  it('sends nothing when the confirmation is cancelled', async () => {
    const fetchMock = stubApi((_url, init) => {
      if (init.method === 'PATCH') return jsonResponse(refund('SUCCESS'))
      return jsonResponse(paginated([refund('REQUESTED')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /setujui/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /setujui/i }))
    await userEvent.click(screen.getByRole('button', { name: /batal/i }))

    expect(fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'PATCH')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: /konfirmasi/i })).not.toBeInTheDocument()
  })

  it('maps each button to the right action', async () => {
    const cases = [
      { status: 'REQUESTED' as const, button: /setujui/i, action: 'APPROVE' },
      { status: 'REQUESTED' as const, button: /^tolak$/i, action: 'REJECT' },
      { status: 'APPROVED' as const, button: /cairkan/i, action: 'PROCESS' },
      { status: 'APPROVED' as const, button: /tandai gagal/i, action: 'FAIL' },
    ]

    for (const tc of cases) {
      const fetchMock = stubApi((_url, init) => {
        if (init.method === 'PATCH') return jsonResponse(refund('SUCCESS'))
        return jsonResponse(paginated([refund(tc.status)]))
      })
      const view = renderPage()

      await waitFor(() => expect(screen.getByRole('button', { name: tc.button })).toBeInTheDocument())
      await userEvent.click(screen.getByRole('button', { name: tc.button }))
      await userEvent.click(screen.getByRole('button', { name: /konfirmasi/i }))

      await waitFor(() => {
        const patch = fetchMock.mock.calls.find(([, i]) => (i as RequestInit)?.method === 'PATCH')
        expect(JSON.parse(String((patch![1] as RequestInit).body)), tc.action).toEqual({
          action: tc.action,
        })
      })
      view.unmount()
    }
  })
})

describe('error handling', () => {
  /** An illegal transition returns 422; the message must land next to the row. */
  it('shows the server message when the transition is rejected', async () => {
    stubApi((_url, init) => {
      if (init.method === 'PATCH') {
        return errorResponse(422, 'INVALID_STATE', 'invalid state transition')
      }
      return jsonResponse(paginated([refund('APPROVED')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /cairkan/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /cairkan/i }))
    await userEvent.click(screen.getByRole('button', { name: /konfirmasi/i }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(/invalid state transition/i),
    )
  })

  it('surfaces insufficient funds distinctly', async () => {
    // Refunding more than the merchant holds is a real outcome the admin must understand.
    stubApi((_url, init) => {
      if (init.method === 'PATCH') {
        return errorResponse(422, 'INSUFFICIENT_FUNDS', 'wallet balance is insufficient')
      }
      return jsonResponse(paginated([refund('APPROVED')]))
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /cairkan/i })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /cairkan/i }))
    await userEvent.click(screen.getByRole('button', { name: /konfirmasi/i }))

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/insufficient/i))
  })
})

describe('list states (SRS §4.1)', () => {
  it('shows an empty state when there are no refunds', async () => {
    stubApi(() => jsonResponse(paginated([])))
    renderPage()

    await waitFor(() =>
      expect(screen.getByText(/belum ada pengajuan refund/i)).toBeInTheDocument(),
    )
  })

  it('shows an error state with retry when the list fails', async () => {
    stubApi(() => errorResponse(500, 'INTERNAL', 'internal server error'))
    renderPage()

    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /coba lagi/i })).toBeInTheDocument()
  })

  it('renders the rows with amount and reason', async () => {
    stubApi(() => jsonResponse(paginated([refund('REQUESTED', { amount: 2500 })])))
    renderPage()

    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    const table = screen.getByRole('table')
    expect(within(table).getByText(/2\.500/)).toBeInTheDocument()
    expect(within(table).getByText('Pelanggan membatalkan')).toBeInTheDocument()
  })
})
