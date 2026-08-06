import type { ApiErrorBody, LoginResponse } from './types'

/**
 * HTTP client for the Payment Sandbox API.
 *
 * Two things here are load-bearing and easy to get wrong:
 *
 *  1. **Refresh is single-flight.** The backend treats a refresh token presented twice as
 *     evidence of theft and revokes every session for that user. If two requests expire
 *     at the same moment and each calls /auth/refresh with the same token, the second one
 *     trips that defence and logs the user out of everything. So concurrent refreshes
 *     share one in-flight promise.
 *
 *  2. **One retry, never a loop.** A request that 401s is retried exactly once after a
 *     successful refresh. If it 401s again the session is genuinely dead, and retrying
 *     further would spin.
 */

/** Errors that carry the backend's `{error:{code,message}}` envelope. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }

  /** True when the caller supplied something invalid, as opposed to a server fault. */
  get isClientError(): boolean {
    return this.status >= 400 && this.status < 500
  }
}

/** Raised when the network never reached the API (offline, DNS, connection refused). */
export class NetworkError extends Error {
  constructor(cause?: unknown) {
    super('Tidak dapat menghubungi server. Periksa koneksi Anda.')
    this.name = 'NetworkError'
    this.cause = cause
  }
}

/** The token pair the client needs; a subset of LoginResponse. */
export interface Tokens {
  accessToken: string
  refreshToken: string
}

/**
 * Callbacks the client uses to read and write session state. Injected rather than
 * imported so the client has no dependency on the store — which is what lets it be
 * tested in isolation.
 */
export interface SessionBridge {
  getTokens: () => Tokens | null
  /** Called after a successful rotation, with the new pair. */
  onRefreshed: (login: LoginResponse) => void
  /** Called when the session cannot be recovered and the user must log in again. */
  onSessionExpired: () => void
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'
  body?: unknown
  /** Query parameters; undefined, null and '' entries are dropped. */
  query?: Record<string, string | number | undefined | null>
  /** Skip the Authorization header and the refresh-on-401 flow (public endpoints). */
  public?: boolean
  signal?: AbortSignal
}

const DEFAULT_ERROR_MESSAGE = 'Terjadi kesalahan yang tidak terduga.'

export class ApiClient {
  private readonly baseUrl: string
  private session: SessionBridge | null = null

  /** The in-flight refresh, shared by every request that hits a 401 concurrently. */
  private refreshInFlight: Promise<Tokens | null> | null = null

  constructor(baseUrl = '') {
    // Trailing slashes would produce '//api/v1/...' once joined.
    this.baseUrl = baseUrl.replace(/\/+$/, '')
  }

  /** Wires session state in. Called once during app bootstrap. */
  attachSession(bridge: SessionBridge): void {
    this.session = bridge
  }

  async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const response = await this.send(path, options)

    // A 401 on an authenticated request means the access token expired. Refresh once,
    // then replay. Public endpoints are excluded: there is no session to recover, and
    // attempting one would burn a refresh token for an anonymous payer.
    if (response.status === 401 && !options.public && this.session?.getTokens()) {
      const refreshed = await this.refreshOnce()
      if (!refreshed) {
        this.session.onSessionExpired()
        throw await toError(response)
      }
      const replay = await this.send(path, options)
      return this.parse<T>(replay)
    }

    return this.parse<T>(response)
  }

  get<T>(path: string, options: Omit<RequestOptions, 'method' | 'body'> = {}) {
    return this.request<T>(path, { ...options, method: 'GET' })
  }

  post<T>(path: string, body?: unknown, options: Omit<RequestOptions, 'method' | 'body'> = {}) {
    return this.request<T>(path, { ...options, method: 'POST', body })
  }

  patch<T>(path: string, body?: unknown, options: Omit<RequestOptions, 'method' | 'body'> = {}) {
    return this.request<T>(path, { ...options, method: 'PATCH', body })
  }

  /**
   * refreshOnce collapses concurrent refresh attempts into one network call.
   *
   * Without this, N requests expiring together would each POST /auth/refresh with the
   * same token. The first rotates it; the rest present an already-revoked token, which
   * the backend reads as a stolen-token replay and answers by revoking every session the
   * user has. The bug would look like "users get randomly logged out under load" and be
   * very hard to trace back here.
   */
  private refreshOnce(): Promise<Tokens | null> {
    if (this.refreshInFlight) return this.refreshInFlight

    this.refreshInFlight = this.performRefresh().finally(() => {
      this.refreshInFlight = null
    })
    return this.refreshInFlight
  }

  private async performRefresh(): Promise<Tokens | null> {
    const tokens = this.session?.getTokens()
    if (!tokens) return null

    let response: Response
    try {
      response = await fetch(this.url('/auth/refresh'), {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ refresh_token: tokens.refreshToken }),
      })
    } catch {
      // Offline during a refresh is not a dead session — do not destroy it. The caller
      // surfaces the original failure and the next attempt can succeed.
      return null
    }

    if (!response.ok) return null

    const login = (await response.json()) as LoginResponse
    if (!login?.access_token || !login?.refresh_token) return null

    this.session?.onRefreshed(login)
    return { accessToken: login.access_token, refreshToken: login.refresh_token }
  }

  private async send(path: string, options: RequestOptions): Promise<Response> {
    const headers: Record<string, string> = {}
    if (options.body !== undefined) headers['content-type'] = 'application/json'

    if (!options.public) {
      const token = this.session?.getTokens()?.accessToken
      if (token) headers['authorization'] = `Bearer ${token}`
    }

    try {
      return await fetch(this.url(path, options.query), {
        method: options.method ?? 'GET',
        headers,
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
        ...(options.signal ? { signal: options.signal } : {}),
      })
    } catch (cause) {
      // An aborted request is the caller's own doing, not a network fault, and must
      // propagate unchanged so callers can ignore it.
      if (cause instanceof DOMException && cause.name === 'AbortError') throw cause
      throw new NetworkError(cause)
    }
  }

  private async parse<T>(response: Response): Promise<T> {
    if (!response.ok) throw await toError(response)
    // 204 No Content (logout) has no body to parse.
    if (response.status === 204) return undefined as T
    return (await response.json()) as T
  }

  private url(path: string, query?: RequestOptions['query']): string {
    const url = `${this.baseUrl}/api/v1${path}`
    if (!query) return url

    const params = new URLSearchParams()
    for (const [key, value] of Object.entries(query)) {
      // Dropping empty values keeps '?status=' out of the URL, which the backend would
      // otherwise have to treat as a filter.
      if (value === undefined || value === null || value === '') continue
      params.set(key, String(value))
    }
    const qs = params.toString()
    return qs ? `${url}?${qs}` : url
  }
}

/**
 * toError converts a failed response into an ApiError, falling back gracefully when the
 * body is not the expected envelope — a proxy returning HTML for a 502, for example.
 */
async function toError(response: Response): Promise<ApiError> {
  let code = `HTTP_${response.status}`
  let message = DEFAULT_ERROR_MESSAGE

  try {
    const body = (await response.json()) as ApiErrorBody
    if (body?.error?.code) code = body.error.code
    if (body?.error?.message) message = body.error.message
  } catch {
    // Non-JSON body: keep the generic message rather than showing raw HTML to the user.
  }

  return new ApiError(response.status, code, message)
}

/** The app-wide client. Base URL is empty in dev so the Vite proxy handles it. */
export const api = new ApiClient(import.meta.env['VITE_API_BASE_URL'] ?? '')
