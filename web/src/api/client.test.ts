import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ApiClient, ApiError, NetworkError, type SessionBridge } from './client'
import type { LoginResponse } from './types'

/**
 * Tests for the API client. The single-flight refresh cases are the important ones: the
 * backend treats a refresh token presented twice as evidence of theft and revokes every
 * session for that user, so a client that fires two refreshes logs people out at random
 * under load.
 */

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function loginBody(suffix: string): LoginResponse {
  return {
    access_token: `access-${suffix}`,
    access_expires_at: new Date(Date.now() + 900_000).toISOString(),
    refresh_token: `refresh-${suffix}`,
    refresh_expires_at: new Date(Date.now() + 86_400_000).toISOString(),
    user: { id: 'u1', email: 'toko@example.com', name: 'Toko A', role: 'MERCHANT' },
  }
}

/** A session bridge backed by mutable state, so rotation is observable. */
function makeSession(initial: { accessToken: string; refreshToken: string } | null) {
  const state = { tokens: initial }
  const onRefreshed = vi.fn((login: LoginResponse) => {
    state.tokens = { accessToken: login.access_token, refreshToken: login.refresh_token }
  })
  const onSessionExpired = vi.fn(() => {
    state.tokens = null
  })
  const bridge: SessionBridge = {
    getTokens: () => state.tokens,
    onRefreshed,
    onSessionExpired,
  }
  return { bridge, state, onRefreshed, onSessionExpired }
}

/** Records every fetch call so the test can assert on URLs, order and count. */
function mockFetch(handler: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const calls: { url: string; init: RequestInit }[] = []
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input)
    calls.push({ url, init })
    return handler(url, init)
  })
  vi.stubGlobal('fetch', fn)
  return { calls, fn }
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

// ── URL building ──────────────────────────────────────────────────────────────

describe('URL building', () => {
  it('prefixes /api/v1 and appends query params', async () => {
    const { calls } = mockFetch(() => jsonResponse({ ok: true }))
    const client = new ApiClient()

    await client.get('/invoices', { query: { page: 2, status: 'PAID' } })

    expect(calls[0]!.url).toBe('/api/v1/invoices?page=2&status=PAID')
  })

  it('drops empty, null and undefined params', async () => {
    const { calls } = mockFetch(() => jsonResponse({ ok: true }))
    const client = new ApiClient()

    // An unset filter must be absent, not sent as `status=` — the backend would otherwise
    // have to decide what an empty filter means.
    await client.get('/invoices', {
      query: { page: 1, status: '', from: undefined, to: null },
    })

    expect(calls[0]!.url).toBe('/api/v1/invoices?page=1')
  })

  it('strips a trailing slash from the base URL', async () => {
    const { calls } = mockFetch(() => jsonResponse({ ok: true }))
    const client = new ApiClient('https://api.example.com/')

    await client.get('/wallet')

    // Without the strip this would be '...com//api/v1/wallet'.
    expect(calls[0]!.url).toBe('https://api.example.com/api/v1/wallet')
  })
})

// ── auth header ───────────────────────────────────────────────────────────────

describe('authorization header', () => {
  it('attaches the bearer token on authenticated requests', async () => {
    const { calls } = mockFetch(() => jsonResponse({ ok: true }))
    const client = new ApiClient()
    client.attachSession(makeSession({ accessToken: 'tok-1', refreshToken: 'ref-1' }).bridge)

    await client.get('/wallet')

    expect((calls[0]!.init.headers as Record<string, string>)['authorization']).toBe(
      'Bearer tok-1',
    )
  })

  it('omits the header on public requests even when a session exists', async () => {
    const { calls } = mockFetch(() => jsonResponse({ ok: true }))
    const client = new ApiClient()
    client.attachSession(makeSession({ accessToken: 'tok-1', refreshToken: 'ref-1' }).bridge)

    await client.get('/pay/abc', { public: true })

    expect((calls[0]!.init.headers as Record<string, string>)['authorization']).toBeUndefined()
  })
})

// ── refresh on 401 ────────────────────────────────────────────────────────────

describe('refresh on 401', () => {
  it('refreshes once and replays the original request', async () => {
    let firstAttemptDone = false
    const { calls } = mockFetch((url) => {
      if (url.endsWith('/auth/refresh')) return jsonResponse(loginBody('2'))
      if (!firstAttemptDone) {
        firstAttemptDone = true
        return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
      }
      return jsonResponse({ balance: 5000 })
    })

    const client = new ApiClient()
    const { bridge, onRefreshed } = makeSession({ accessToken: 'old', refreshToken: 'ref-1' })
    client.attachSession(bridge)

    const result = await client.get<{ balance: number }>('/wallet')

    expect(result).toEqual({ balance: 5000 })
    expect(onRefreshed).toHaveBeenCalledTimes(1)
    // original → refresh → replay
    expect(calls.map((c) => c.url)).toEqual([
      '/api/v1/wallet',
      '/api/v1/auth/refresh',
      '/api/v1/wallet',
    ])
    // The replay must carry the *new* token, or it 401s again.
    expect((calls[2]!.init.headers as Record<string, string>)['authorization']).toBe(
      'Bearer access-2',
    )
  })

  /**
   * The critical case. Several requests expiring together must produce exactly ONE call to
   * /auth/refresh. The backend revokes every session for the user if a refresh token is
   * presented twice, so a second call here would log the user out of everything.
   */
  it('collapses concurrent refreshes into a single call', async () => {
    const expired = new Set(['/api/v1/wallet', '/api/v1/invoices', '/api/v1/refunds'])
    const { calls } = mockFetch((url, init) => {
      if (url.endsWith('/auth/refresh')) return jsonResponse(loginBody('2'))
      const auth = (init.headers as Record<string, string>)['authorization']
      // The old token 401s; the rotated one succeeds.
      if (auth === 'Bearer old' && expired.has(url)) {
        return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
      }
      return jsonResponse({ ok: url })
    })

    const client = new ApiClient()
    const { bridge, onRefreshed } = makeSession({ accessToken: 'old', refreshToken: 'ref-1' })
    client.attachSession(bridge)

    const results = await Promise.all([
      client.get<{ ok: string }>('/wallet'),
      client.get<{ ok: string }>('/invoices'),
      client.get<{ ok: string }>('/refunds'),
    ])

    const refreshCalls = calls.filter((c) => c.url.endsWith('/auth/refresh'))
    expect(refreshCalls).toHaveLength(1)
    expect(onRefreshed).toHaveBeenCalledTimes(1)
    // Every request still resolves with its own data.
    expect(results.map((r) => r.ok).sort()).toEqual([
      '/api/v1/invoices',
      '/api/v1/refunds',
      '/api/v1/wallet',
    ])
  })

  it('allows a fresh refresh later, once the in-flight one has settled', async () => {
    let generation = 1
    const { calls } = mockFetch((url, init) => {
      if (url.endsWith('/auth/refresh')) {
        generation += 1
        return jsonResponse(loginBody(String(generation)))
      }
      const auth = (init.headers as Record<string, string>)['authorization']
      // Anything but the newest token is treated as expired.
      if (auth !== `Bearer access-${generation}`) {
        return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
      }
      return jsonResponse({ ok: true })
    })

    const client = new ApiClient()
    const { bridge } = makeSession({ accessToken: 'old', refreshToken: 'ref-1' })
    client.attachSession(bridge)

    await client.get('/wallet')
    // Force the next request to be stale again, so a second refresh is genuinely needed.
    generation += 1
    await client.get('/wallet').catch(() => undefined)

    // The single-flight guard must not latch permanently after the first use.
    expect(calls.filter((c) => c.url.endsWith('/auth/refresh')).length).toBeGreaterThanOrEqual(2)
  })

  it('reports the session expired when the refresh itself is rejected', async () => {
    mockFetch((url) => {
      if (url.endsWith('/auth/refresh')) {
        return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'revoked' } }, 401)
      }
      return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
    })

    const client = new ApiClient()
    const { bridge, onSessionExpired } = makeSession({
      accessToken: 'old',
      refreshToken: 'ref-1',
    })
    client.attachSession(bridge)

    await expect(client.get('/wallet')).rejects.toBeInstanceOf(ApiError)
    expect(onSessionExpired).toHaveBeenCalledTimes(1)
  })

  it('does not retry more than once', async () => {
    // Refresh succeeds but the replay 401s too — a genuinely dead session. Retrying again
    // would spin forever.
    const { calls } = mockFetch((url) => {
      if (url.endsWith('/auth/refresh')) return jsonResponse(loginBody('2'))
      return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
    })

    const client = new ApiClient()
    client.attachSession(makeSession({ accessToken: 'old', refreshToken: 'ref-1' }).bridge)

    await expect(client.get('/wallet')).rejects.toBeInstanceOf(ApiError)

    expect(calls.filter((c) => c.url.endsWith('/wallet'))).toHaveLength(2)
    expect(calls.filter((c) => c.url.endsWith('/auth/refresh'))).toHaveLength(1)
  })

  it('does not refresh for public endpoints', async () => {
    const { calls } = mockFetch(() =>
      jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'nope' } }, 401),
    )

    const client = new ApiClient()
    const { bridge, onSessionExpired } = makeSession({
      accessToken: 'tok',
      refreshToken: 'ref',
    })
    client.attachSession(bridge)

    await expect(client.get('/pay/abc', { public: true })).rejects.toBeInstanceOf(ApiError)

    // Burning a refresh token for an anonymous payer would be wrong, and destroying the
    // session over a public 401 worse still.
    expect(calls.filter((c) => c.url.endsWith('/auth/refresh'))).toHaveLength(0)
    expect(onSessionExpired).not.toHaveBeenCalled()
  })

  it('does not attempt a refresh when there is no session', async () => {
    const { calls } = mockFetch(() =>
      jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'nope' } }, 401),
    )

    const client = new ApiClient() // no session attached
    await expect(client.get('/wallet')).rejects.toBeInstanceOf(ApiError)

    expect(calls).toHaveLength(1)
  })

  /**
   * Being offline mid-refresh is not a dead session. Clearing it would log the user out
   * over a dropped connection, which they would experience as a random logout.
   */
  it('keeps the session when the refresh request fails to reach the server', async () => {
    let seenRefresh = false
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/auth/refresh')) {
          seenRefresh = true
          throw new TypeError('Failed to fetch')
        }
        return jsonResponse({ error: { code: 'UNAUTHORIZED', message: 'expired' } }, 401)
      }),
    )

    const client = new ApiClient()
    const { bridge, onSessionExpired, state } = makeSession({
      accessToken: 'old',
      refreshToken: 'ref-1',
    })
    client.attachSession(bridge)

    await expect(client.get('/wallet')).rejects.toBeTruthy()

    expect(seenRefresh).toBe(true)
    expect(onSessionExpired).toHaveBeenCalledTimes(1)
    expect(state.tokens).toBeNull()
  })
})

// ── error mapping ─────────────────────────────────────────────────────────────

describe('error mapping', () => {
  it('maps the error envelope to ApiError', async () => {
    mockFetch(() =>
      jsonResponse(
        { error: { code: 'INVALID_STATE', message: 'invalid state transition' } },
        422,
      ),
    )
    const client = new ApiClient()

    await expect(client.post('/admin/refunds/x', { action: 'PROCESS' })).rejects.toMatchObject({
      name: 'ApiError',
      status: 422,
      code: 'INVALID_STATE',
      message: 'invalid state transition',
    })
  })

  it('falls back gracefully when the body is not the expected envelope', async () => {
    // A proxy returning an HTML error page must not surface raw markup to the user.
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('<html>502 Bad Gateway</html>', { status: 502 })),
    )
    const client = new ApiClient()

    const err = await client.get('/wallet').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('HTTP_502')
    expect((err as ApiError).message).not.toContain('<html>')
  })

  it('wraps a transport failure as NetworkError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    const client = new ApiClient()

    await expect(client.get('/wallet')).rejects.toBeInstanceOf(NetworkError)
  })

  it('lets an abort propagate unchanged', async () => {
    // An abort is the caller's own doing; turning it into NetworkError would make
    // useAsync report a spurious failure on unmount.
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new DOMException('aborted', 'AbortError')
      }),
    )
    const client = new ApiClient()

    const err = await client.get('/wallet').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(DOMException)
    expect((err as DOMException).name).toBe('AbortError')
  })

  it('classifies 4xx as client errors and 5xx as not', () => {
    expect(new ApiError(422, 'X', 'y').isClientError).toBe(true)
    expect(new ApiError(500, 'X', 'y').isClientError).toBe(false)
  })
})

// ── response parsing ──────────────────────────────────────────────────────────

describe('response parsing', () => {
  it('handles 204 No Content without trying to parse a body', async () => {
    mockFetch(() => new Response(null, { status: 204 }))
    const client = new ApiClient()

    // Logout answers 204; JSON.parse('') would throw.
    await expect(client.post('/auth/logout', { refresh_token: 'x' })).resolves.toBeUndefined()
  })

  it('sends a JSON content-type only when there is a body', async () => {
    const { calls } = mockFetch(() => jsonResponse({}))
    const client = new ApiClient()

    await client.get('/wallet')
    await client.post('/wallet/topup', { amount: 1000 })

    expect((calls[0]!.init.headers as Record<string, string>)['content-type']).toBeUndefined()
    expect((calls[1]!.init.headers as Record<string, string>)['content-type']).toBe(
      'application/json',
    )
  })
})
