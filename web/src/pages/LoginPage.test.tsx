import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route } from 'react-router-dom'
import { LoginPage } from './LoginPage'
import { errorResponse, jsonResponse, renderRoute, signOut } from '@/test/render'
import { useAuth } from '@/store/auth'
import type { LoginResponse } from '@/api/types'

/**
 * Login form: validation (SRS §4.5), error states (§4.1), and role-based landing (§2.1).
 */

function loginResponse(role: 'MERCHANT' | 'ADMIN' = 'MERCHANT'): LoginResponse {
  return {
    access_token: 'access-1',
    access_expires_at: new Date(Date.now() + 900_000).toISOString(),
    refresh_token: 'refresh-1',
    refresh_expires_at: new Date(Date.now() + 86_400_000).toISOString(),
    user: {
      id: '11111111-1111-1111-1111-111111111111',
      email: 'toko@example.com',
      name: 'Toko A',
      role,
    },
  }
}

function stubApi(handler: (url: string, init: RequestInit) => Response) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) =>
    handler(String(input), init),
  )
  vi.stubGlobal('fetch', fn)
  return fn
}

const landings = (
  <>
    <Route path="/merchant" element={<p>merchant home</p>} />
    <Route path="/admin" element={<p>admin home</p>} />
  </>
)

function renderPage() {
  return renderRoute(<LoginPage />, { path: '/login', extraRoutes: landings })
}

beforeEach(signOut)

describe('client-side validation (SRS §4.5)', () => {
  it('rejects a malformed email without calling the API', async () => {
    const fetchMock = stubApi(() => jsonResponse(loginResponse()))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'not-an-email')
    await userEvent.type(screen.getByLabelText(/password/i), 'password123')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    expect(await screen.findByText(/format email tidak valid/i)).toBeInTheDocument()
    // Validation must short-circuit: a request known to fail is wasted and, on an endpoint
    // that is rate limited, actively harmful.
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('requires both fields', async () => {
    const fetchMock = stubApi(() => jsonResponse(loginResponse()))
    renderPage()

    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    expect(await screen.findByText(/email wajib diisi/i)).toBeInTheDocument()
    expect(screen.getByText(/password wajib diisi/i)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  /**
   * Length rules deliberately do not apply at login. Telling a caller their password is
   * "too short" reveals which guesses are the wrong shape.
   */
  it('does not impose a length rule on the password', async () => {
    const fetchMock = stubApi(() => jsonResponse(loginResponse()))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'abc')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(screen.queryByText(/minimal 8 karakter/i)).not.toBeInTheDocument()
  })

  it('clears a field error as soon as it is edited', async () => {
    stubApi(() => jsonResponse(loginResponse()))
    renderPage()

    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))
    expect(await screen.findByText(/email wajib diisi/i)).toBeInTheDocument()

    // Nagging while the user is mid-correction is noise.
    await userEvent.type(screen.getByLabelText(/email/i), 'a')
    expect(screen.queryByText(/email wajib diisi/i)).not.toBeInTheDocument()
  })

  it('marks the invalid field for assistive tech, not just visually', async () => {
    stubApi(() => jsonResponse(loginResponse()))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'bad')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    const email = screen.getByLabelText(/email/i)
    await waitFor(() => expect(email).toHaveAttribute('aria-invalid', 'true'))
    // The message must be linked, or a screen-reader user hears nothing about why.
    expect(email).toHaveAttribute('aria-describedby')
  })
})

describe('successful login', () => {
  it('stores the session and lands a merchant on their dashboard', async () => {
    stubApi(() => jsonResponse(loginResponse('MERCHANT')))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'password123')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    expect(await screen.findByText('merchant home')).toBeInTheDocument()
    const state = useAuth.getState()
    expect(state.accessToken).toBe('access-1')
    expect(state.refreshToken).toBe('refresh-1')
    expect(state.user?.role).toBe('MERCHANT')
  })

  /** SRS §2.1: page access depends on role, so the landing page does too. */
  it('lands an admin in the admin area', async () => {
    stubApi(() => jsonResponse(loginResponse('ADMIN')))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'admin12345')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    expect(await screen.findByText('admin home')).toBeInTheDocument()
  })

  it('trims the email before sending it', async () => {
    const fetchMock = stubApi(() => jsonResponse(loginResponse()))
    renderPage()

    // A trailing space from autofill or a paste must not become a different account.
    await userEvent.type(screen.getByLabelText(/email/i), '  toko@example.com  ')
    await userEvent.type(screen.getByLabelText(/password/i), 'password123')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const body = JSON.parse(String((fetchMock.mock.calls[0]![1] as RequestInit).body))
    expect(body.email).toBe('toko@example.com')
  })
})

describe('failed login (SRS §4.1 error state)', () => {
  it('shows the credentials error without revealing whether the account exists', async () => {
    stubApi(() => errorResponse(401, 'INVALID_CREDENTIALS', 'email or password is incorrect'))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong-password')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/email or password is incorrect/i)
    // The backend deliberately returns one message for both cases; the UI must not
    // paraphrase it into something more specific.
    expect(alert).not.toHaveTextContent(/tidak ditemukan|not found/i)
    expect(useAuth.getState().accessToken).toBeNull()
  })

  /** The backend rate limits login per IP and per account; 429 needs its own guidance. */
  it('explains a rate-limited response rather than showing a raw error', async () => {
    stubApi(() => errorResponse(429, 'RATE_LIMITED', 'too many requests, please retry later'))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'password123')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    const alert = await screen.findByRole('status')
    expect(alert).toHaveTextContent(/terlalu banyak percobaan/i)
  })

  it('reports an unreachable server distinctly from a rejected login', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'password123')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/tidak dapat menghubungi server/i)
  })

  it('clears the previous error when the user edits a field', async () => {
    stubApi(() => errorResponse(401, 'INVALID_CREDENTIALS', 'email or password is incorrect'))
    renderPage()

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()

    await userEvent.type(screen.getByLabelText(/password/i), 'x')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('submit protection', () => {
  /**
   * A double-submit on login burns two attempts against a rate limit that counts per
   * account, so the button must disable itself while a request is in flight.
   */
  it('disables the button while the request is in flight', async () => {
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

    await userEvent.type(screen.getByLabelText(/email/i), 'toko@example.com')
    await userEvent.type(screen.getByLabelText(/password/i), 'password123')
    await userEvent.click(screen.getByRole('button', { name: /masuk/i }))

    const button = screen.getByRole('button', { name: /masuk/i })
    await waitFor(() => expect(button).toBeDisabled())
    expect(button).toHaveAttribute('aria-busy', 'true')

    release(jsonResponse(loginResponse()))
  })
})
