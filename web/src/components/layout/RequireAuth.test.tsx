import { beforeEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { Route } from 'react-router-dom'
import { RedirectIfAuthenticated, RequireAuth } from './RequireAuth'
import { renderRoute, signIn, signOut } from '@/test/render'

/**
 * Route guard behaviour (SRS §2.1: page access depends on role).
 *
 * These assert navigation, not security. Every protected endpoint is enforced by the
 * backend's JWT + RequireRole middleware; a user who edited their stored role would still
 * get a 403 from the API. The guard exists so nobody is shown a page that would fail, and
 * so a deep link taken while logged out lands somewhere sensible.
 */

const Protected = () => <p>secret content</p>
const LoginStub = () => <p>login page</p>
const MerchantHome = () => <p>merchant home</p>
const AdminHome = () => <p>admin home</p>

const landingRoutes = (
  <>
    <Route path="/login" element={<LoginStub />} />
    <Route path="/merchant" element={<MerchantHome />} />
    <Route path="/admin" element={<AdminHome />} />
  </>
)

beforeEach(signOut)

describe('RequireAuth', () => {
  it('redirects an anonymous visitor to login', () => {
    renderRoute(
      <RequireAuth>
        <Protected />
      </RequireAuth>,
      { path: '/merchant/invoices', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('login page')).toBeInTheDocument()
    expect(screen.queryByText('secret content')).not.toBeInTheDocument()
  })

  it('renders the page for a signed-in user when no role is required', () => {
    signIn('MERCHANT')
    renderRoute(
      <RequireAuth>
        <Protected />
      </RequireAuth>,
      { path: '/merchant', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('secret content')).toBeInTheDocument()
  })

  it('allows a matching role', () => {
    signIn('ADMIN')
    renderRoute(
      <RequireAuth allow={['ADMIN']}>
        <Protected />
      </RequireAuth>,
      { path: '/admin/payments', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('secret content')).toBeInTheDocument()
  })

  /**
   * A merchant reaching an admin route is sent to their own home rather than shown a dead
   * end — and the forbidden URL is replaced in history so Back does not bounce them here.
   */
  it('sends a merchant away from an admin route', () => {
    signIn('MERCHANT')
    renderRoute(
      <RequireAuth allow={['ADMIN']}>
        <Protected />
      </RequireAuth>,
      { path: '/admin', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('merchant home')).toBeInTheDocument()
    expect(screen.queryByText('secret content')).not.toBeInTheDocument()
  })

  it('sends an admin away from a merchant route', () => {
    signIn('ADMIN')
    renderRoute(
      <RequireAuth allow={['MERCHANT']}>
        <Protected />
      </RequireAuth>,
      { path: '/merchant', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('admin home')).toBeInTheDocument()
  })

  it('accepts any role in the allow-list', () => {
    signIn('MERCHANT')
    renderRoute(
      <RequireAuth allow={['ADMIN', 'MERCHANT']}>
        <Protected />
      </RequireAuth>,
      { path: '/shared', route: '/shared', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('secret content')).toBeInTheDocument()
  })

  /**
   * A token without a user object is not a session. Treating it as one would render pages
   * that immediately fail on the first API call.
   */
  it('treats a half-populated store as unauthenticated', () => {
    signOut()
    renderRoute(
      <RequireAuth>
        <Protected />
      </RequireAuth>,
      { path: '/merchant', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('login page')).toBeInTheDocument()
  })
})

describe('RedirectIfAuthenticated', () => {
  it('lets an anonymous visitor see the login page', () => {
    renderRoute(
      <RedirectIfAuthenticated>
        <LoginStub />
      </RedirectIfAuthenticated>,
      { path: '/login', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('login page')).toBeInTheDocument()
  })

  it('bounces a signed-in merchant to their dashboard', () => {
    signIn('MERCHANT')
    renderRoute(
      <RedirectIfAuthenticated>
        <LoginStub />
      </RedirectIfAuthenticated>,
      { path: '/login', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('merchant home')).toBeInTheDocument()
  })

  it('bounces a signed-in admin to the admin area', () => {
    signIn('ADMIN')
    renderRoute(
      <RedirectIfAuthenticated>
        <LoginStub />
      </RedirectIfAuthenticated>,
      { path: '/login', extraRoutes: landingRoutes },
    )

    expect(screen.getByText('admin home')).toBeInTheDocument()
  })
})
