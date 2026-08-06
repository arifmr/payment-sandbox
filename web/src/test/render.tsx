import type { ReactElement, ReactNode } from 'react'
import { render, type RenderResult } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { useAuth } from '@/store/auth'
import type { Role, User } from '@/api/types'

/**
 * Test helpers for rendering routed components.
 *
 * MemoryRouter is used rather than BrowserRouter so tests can start at an arbitrary path
 * and assert on redirects without touching window.history.
 */

/** Puts a signed-in user into the store, as a real login would. */
export function signIn(role: Role = 'MERCHANT', overrides: Partial<User> = {}): User {
  const user: User = {
    id: '11111111-1111-1111-1111-111111111111',
    email: role === 'ADMIN' ? 'admin@example.com' : 'toko@example.com',
    name: role === 'ADMIN' ? 'Admin' : 'Toko A',
    role,
    ...overrides,
  }
  useAuth.setState({ user, accessToken: 'test-access', refreshToken: 'test-refresh' })
  return user
}

/** Clears the session. Call in beforeEach so tests do not inherit each other's login. */
export function signOut(): void {
  useAuth.setState({ user: null, accessToken: null, refreshToken: null, busy: false })
}

export interface RenderRouteOptions {
  /** Initial URL. */
  path?: string
  /** Route pattern for the element under test, when it reads params. */
  route?: string
  /** Extra routes, so a redirect has somewhere to land and can be asserted on. */
  extraRoutes?: ReactNode
}

/**
 * Renders `element` inside a router.
 *
 * `extraRoutes` matters for guard tests: without a destination, a <Navigate> renders
 * nothing and the test cannot tell a redirect from a blank screen.
 */
export function renderRoute(element: ReactElement, options: RenderRouteOptions = {}): RenderResult {
  const { path = '/', route = path, extraRoutes } = options

  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path={route} element={element} />
        {extraRoutes}
      </Routes>
    </MemoryRouter>,
  )
}

/** A JSON Response, matching what the API client expects. */
export function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

/** An error Response in the backend's `{error:{code,message}}` envelope. */
export function errorResponse(status: number, code: string, message: string): Response {
  return jsonResponse({ error: { code, message } }, status)
}

/** A paginated envelope, matching the backend's list responses. */
export function paginated<T>(items: T[], total = items.length, page = 1, pageSize = 20) {
  return { data: items, pagination: { page, page_size: pageSize, total } }
}
