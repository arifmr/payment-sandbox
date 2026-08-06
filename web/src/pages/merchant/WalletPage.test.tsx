import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WalletPage } from './WalletPage'
import { errorResponse, jsonResponse, paginated, renderRoute, signIn, signOut } from '@/test/render'
import type { Topup, TopupStatus, Wallet } from '@/api/types'

/**
 * Wallet and top-up simulation (SRS §2.2 / §4.2).
 *
 * The rule that matters: a top-up is created PENDING and must **not** change the balance
 * until an admin settles it. A UI that optimistically adds the amount would tell the
 * merchant they have money they cannot spend.
 */

const wallet: Wallet = {
  merchant_id: '11111111-1111-1111-1111-111111111111',
  balance: 7500,
  updated_at: '2026-07-26T10:00:00Z',
}

function topup(status: TopupStatus, overrides: Partial<Topup> = {}): Topup {
  return {
    id: '22222222-2222-2222-2222-222222222222',
    merchant_id: wallet.merchant_id,
    amount: 100000,
    status,
    created_at: '2026-07-26T09:00:00Z',
    ...overrides,
  }
}

/** Routes by URL so balance and top-up list can be stubbed independently. */
function stubApi(options: {
  walletResponse?: () => Response
  topupsResponse?: () => Response
  createResponse?: () => Response
}) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input)
    if (init.method === 'POST' && url.includes('/wallet/topup')) {
      return options.createResponse?.() ?? jsonResponse(topup('PENDING'), 201)
    }
    if (url.includes('/wallet/topups')) {
      return options.topupsResponse?.() ?? jsonResponse(paginated([]))
    }
    return options.walletResponse?.() ?? jsonResponse(wallet)
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

function renderPage() {
  return renderRoute(<WalletPage />, { path: '/merchant/wallet' })
}

beforeEach(() => {
  signOut()
  signIn('MERCHANT')
})

describe('balance', () => {
  it('shows the current balance', async () => {
    stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByText(/7\.500/)).toBeInTheDocument())
    expect(screen.getByText(/saldo saat ini/i)).toBeInTheDocument()
  })

  it('offers a retry when the balance cannot be loaded', async () => {
    let attempts = 0
    stubApi({
      walletResponse: () => {
        attempts += 1
        return attempts === 1
          ? errorResponse(500, 'INTERNAL', 'internal server error')
          : jsonResponse(wallet)
      },
    })
    renderPage()

    // A failed balance must not blank the page — the top-up form still works.
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    await userEvent.click(within(screen.getByRole('alert')).getByRole('button', { name: /coba lagi/i }))

    await waitFor(() => expect(screen.getByText(/7\.500/)).toBeInTheDocument())
  })
})

describe('requesting a top-up (SRS §2.2)', () => {
  it('sends the amount and confirms it is pending', async () => {
    const fetchMock = stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toBeInTheDocument())
    await userEvent.type(screen.getByLabelText(/nominal top-up/i), '100000')
    await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

    await waitFor(() => {
      const post = fetchMock.mock.calls.find(([, i]) => (i as RequestInit)?.method === 'POST')
      expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({ amount: 100000 })
    })

    // The confirmation must say the balance has not moved yet.
    expect(await screen.findByRole('status')).toHaveTextContent(/setelah admin menyetujui/i)
  })

  /**
   * The balance shown must come from the server, never from optimistically adding the
   * requested amount — the top-up is PENDING and may yet be rejected.
   */
  it('does not change the displayed balance after requesting', async () => {
    stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByText(/7\.500/)).toBeInTheDocument())
    await userEvent.type(screen.getByLabelText(/nominal top-up/i), '100000')
    await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

    await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())
    expect(screen.getByText(/7\.500/)).toBeInTheDocument()
    expect(screen.queryByText(/107\.500/)).not.toBeInTheDocument()
  })

  it('clears the input after a successful request', async () => {
    stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toBeInTheDocument())
    await userEvent.type(screen.getByLabelText(/nominal top-up/i), '100000')
    await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

    // Leaving the value would invite an accidental duplicate submission.
    await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toHaveValue(null))
  })

  it('refreshes the history so the new request appears', async () => {
    const fetchMock = stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toBeInTheDocument())
    const listCallsBefore = fetchMock.mock.calls.filter(([u]) =>
      String(u).includes('/wallet/topups'),
    ).length

    await userEvent.type(screen.getByLabelText(/nominal top-up/i), '100000')
    await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

    await waitFor(() => {
      const after = fetchMock.mock.calls.filter(([u]) => String(u).includes('/wallet/topups')).length
      expect(after).toBeGreaterThan(listCallsBefore)
    })
  })
})

describe('top-up validation (SRS §4.5)', () => {
  it('rejects a non-positive amount without calling the API', async () => {
    for (const value of ['0', '-500']) {
      const fetchMock = stubApi({})
      const view = renderPage()

      await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toBeInTheDocument())
      await userEvent.type(screen.getByLabelText(/nominal top-up/i), value)
      await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

      expect(await screen.findByText(/lebih besar dari 0/i), value).toBeInTheDocument()
      expect(
        fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'POST'),
        value,
      ).toHaveLength(0)

      view.unmount()
    }
  })

  it('requires an amount', async () => {
    const fetchMock = stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

    expect(await screen.findByText(/nominal wajib diisi/i)).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(([, i]) => (i as RequestInit)?.method === 'POST')).toHaveLength(0)
  })

  it('shows a server rejection', async () => {
    stubApi({
      createResponse: () => errorResponse(400, 'INVALID_AMOUNT', 'amount must be greater than zero'),
    })
    renderPage()

    await waitFor(() => expect(screen.getByLabelText(/nominal top-up/i)).toBeInTheDocument())
    await userEvent.type(screen.getByLabelText(/nominal top-up/i), '100000')
    await userEvent.click(screen.getByRole('button', { name: /ajukan top-up/i }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(/amount must be greater than zero/i),
    )
  })
})

describe('history (SRS §4.1 states)', () => {
  it('shows an empty state when there are no top-ups', async () => {
    stubApi({})
    renderPage()

    await waitFor(() => expect(screen.getByText(/belum ada top-up/i)).toBeInTheDocument())
  })

  it('lists top-ups with their status', async () => {
    stubApi({
      topupsResponse: () =>
        jsonResponse(
          paginated([
            topup('SUCCESS', { id: 'a', amount: 50000, processed_at: '2026-07-26T09:30:00Z' }),
            topup('PENDING', { id: 'b', amount: 25000 }),
          ]),
        ),
    })
    renderPage()

    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    const table = screen.getByRole('table')
    expect(within(table).getByText(/50\.000/)).toBeInTheDocument()
    expect(within(table).getByText(/25\.000/)).toBeInTheDocument()
    expect(within(table).getByText('Berhasil')).toBeInTheDocument()
    expect(within(table).getByText('Menunggu')).toBeInTheDocument()
  })

  it('shows an error state with retry when the history fails', async () => {
    stubApi({ topupsResponse: () => errorResponse(500, 'INTERNAL', 'internal server error') })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /coba lagi/i })).toBeInTheDocument())
  })
})
