import { beforeEach, describe, expect, it, vi } from 'vitest'
import { connectAuthToApi, homePathFor, useAuth } from './auth'
import { api } from '@/api/client'
import { errorResponse, jsonResponse } from '@/test/render'
import type { LoginResponse } from '@/api/types'

/**
 * Session store (SRS §4.1: tidy state management).
 *
 * The interesting behaviour is at the edges: a failed logout must still clear local state,
 * and the store must be wired into the API client so the client can rotate tokens without
 * knowing about Zustand.
 */

function loginResponse(role: 'MERCHANT' | 'ADMIN' = 'MERCHANT', suffix = '1'): LoginResponse {
  return {
    access_token: `access-${suffix}`,
    access_expires_at: new Date(Date.now() + 900_000).toISOString(),
    refresh_token: `refresh-${suffix}`,
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

beforeEach(() => {
  useAuth.setState({ user: null, accessToken: null, refreshToken: null, busy: false })
})

describe('login', () => {
  it('stores the user and both tokens', async () => {
    stubApi(() => jsonResponse(loginResponse()))

    const user = await useAuth.getState().login('toko@example.com', 'password123')

    expect(user.role).toBe('MERCHANT')
    const state = useAuth.getState()
    expect(state.accessToken).toBe('access-1')
    expect(state.refreshToken).toBe('refresh-1')
    expect(state.user?.email).toBe('toko@example.com')
  })

  it('clears busy and leaves the session empty when login fails', async () => {
    stubApi(() => errorResponse(401, 'INVALID_CREDENTIALS', 'email or password is incorrect'))

    await expect(useAuth.getState().login('toko@example.com', 'nope')).rejects.toBeTruthy()

    const state = useAuth.getState()
    // A stuck `busy` would leave every submit button spinning forever.
    expect(state.busy).toBe(false)
    expect(state.accessToken).toBeNull()
    expect(state.user).toBeNull()
  })
})

describe('register', () => {
  /** Registering then asking for the same credentials again would be poor UX. */
  it('logs in immediately after registering', async () => {
    const fetchMock = stubApi((url) => {
      if (url.endsWith('/auth/register')) {
        return jsonResponse(loginResponse().user, 201)
      }
      return jsonResponse(loginResponse())
    })

    await useAuth.getState().register('Toko A', 'toko@example.com', 'password123')

    const urls = fetchMock.mock.calls.map(([u]) => String(u))
    expect(urls.some((u) => u.endsWith('/auth/register'))).toBe(true)
    expect(urls.some((u) => u.endsWith('/auth/login'))).toBe(true)
    expect(useAuth.getState().accessToken).toBe('access-1')
  })

  it('propagates a duplicate-email conflict', async () => {
    stubApi(() => errorResponse(409, 'EMAIL_TAKEN', 'email already registered'))

    await expect(
      useAuth.getState().register('Toko A', 'taken@example.com', 'password123'),
    ).rejects.toMatchObject({ code: 'EMAIL_TAKEN' })
    expect(useAuth.getState().busy).toBe(false)
  })
})

describe('logout', () => {
  it('revokes the refresh token server-side', async () => {
    const fetchMock = stubApi(() => new Response(null, { status: 204 }))
    useAuth.setState({
      user: loginResponse().user,
      accessToken: 'access-1',
      refreshToken: 'refresh-1',
    })

    await useAuth.getState().logout()

    const call = fetchMock.mock.calls.find(([u]) => String(u).endsWith('/auth/logout'))
    expect(call).toBeDefined()
    expect(JSON.parse(String((call![1] as RequestInit).body))).toEqual({
      refresh_token: 'refresh-1',
    })
    expect(useAuth.getState().accessToken).toBeNull()
  })

  /**
   * A failed revoke must not trap the user in a logged-in UI. The token expires on its own
   * regardless, so clearing locally is the right call.
   */
  it('clears local state even when the revoke call fails', async () => {
    stubApi(() => errorResponse(500, 'INTERNAL', 'internal server error'))
    useAuth.setState({
      user: loginResponse().user,
      accessToken: 'access-1',
      refreshToken: 'refresh-1',
    })

    await expect(useAuth.getState().logout()).resolves.toBeUndefined()

    const state = useAuth.getState()
    expect(state.user).toBeNull()
    expect(state.accessToken).toBeNull()
    expect(state.busy).toBe(false)
  })

  it('clears local state when the server is unreachable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    useAuth.setState({
      user: loginResponse().user,
      accessToken: 'access-1',
      refreshToken: 'refresh-1',
    })

    await useAuth.getState().logout()
    expect(useAuth.getState().user).toBeNull()
  })

  it('is a no-op without a session', async () => {
    const fetchMock = stubApi(() => new Response(null, { status: 204 }))
    await useAuth.getState().logout()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('persistence', () => {
  it('persists the session but not the transient busy flag', async () => {
    stubApi(() => jsonResponse(loginResponse()))
    await useAuth.getState().login('toko@example.com', 'password123')

    const raw = window.localStorage.getItem('payment-sandbox.session')
    expect(raw).toBeTruthy()
    const persisted = JSON.parse(raw!) as { state: Record<string, unknown> }
    expect(persisted.state.accessToken).toBe('access-1')
    // Persisting `busy` would leave a page reloaded mid-request stuck in a loading state.
    expect(persisted.state).not.toHaveProperty('busy')
  })
})

describe('API client wiring', () => {
  /**
   * The client must read tokens through the bridge rather than importing the store, which
   * is what keeps it independently testable — and what lets it rotate tokens on 401.
   */
  it('exposes the current tokens to the client and applies a rotation', async () => {
    connectAuthToApi()

    useAuth.setState({
      user: loginResponse().user,
      accessToken: 'access-1',
      refreshToken: 'refresh-1',
    })

    let sawAuthHeader: string | undefined
    let refreshed = false
    stubApi((url, init) => {
      if (url.endsWith('/auth/refresh')) {
        refreshed = true
        return jsonResponse(loginResponse('MERCHANT', '2'))
      }
      const auth = (init.headers as Record<string, string>)['authorization']
      sawAuthHeader = auth
      if (auth === 'Bearer access-1') {
        return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
      }
      return jsonResponse({ balance: 1000 })
    })

    await api.get('/wallet')

    expect(refreshed).toBe(true)
    expect(sawAuthHeader).toBe('Bearer access-2')
    // The rotated pair must land in the store, or the next request uses a dead token.
    expect(useAuth.getState().accessToken).toBe('access-2')
    expect(useAuth.getState().refreshToken).toBe('refresh-2')
  })

  it('clears the session when the client reports it unrecoverable', async () => {
    connectAuthToApi()
    useAuth.setState({
      user: loginResponse().user,
      accessToken: 'access-1',
      refreshToken: 'refresh-1',
    })

    stubApi(() => errorResponse(401, 'UNAUTHORIZED', 'revoked'))

    await expect(api.get('/wallet')).rejects.toBeTruthy()
    expect(useAuth.getState().accessToken).toBeNull()
  })

  it('reports no tokens when the session is half-populated', async () => {
    connectAuthToApi()
    // An access token without a refresh token cannot be rotated, so it is not a session.
    useAuth.setState({ user: loginResponse().user, accessToken: 'a', refreshToken: null })

    const fetchMock = stubApi(() => errorResponse(401, 'UNAUTHORIZED', 'expired'))
    await expect(api.get('/wallet')).rejects.toBeTruthy()

    expect(fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/auth/refresh'))).toHaveLength(0)
  })
})

describe('homePathFor', () => {
  it('routes each role to its own area (SRS §2.1)', () => {
    expect(homePathFor('ADMIN')).toBe('/admin')
    expect(homePathFor('MERCHANT')).toBe('/merchant')
    // An unknown or absent role must not land in the admin area.
    expect(homePathFor(null)).toBe('/merchant')
  })
})
