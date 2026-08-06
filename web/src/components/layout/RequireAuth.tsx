import { Navigate, useLocation } from 'react-router-dom'
import type { ReactNode } from 'react'
import { homePathFor, selectIsAuthenticated, selectRole, useAuth } from '@/store/auth'
import type { Role } from '@/api/types'

/**
 * Route guard (SRS §2.1: page access depends on role).
 *
 * This is **navigation only, not security.** Every protected endpoint is enforced by the
 * backend's JWT + RequireRole middleware; a user who edits their stored role would still
 * get a 403 from the API. The guard exists so people are not shown pages that would fail,
 * and so a deep link taken while logged out lands somewhere sensible.
 */
interface RequireAuthProps {
  /** Roles allowed here. Omit to require only that the user is signed in. */
  allow?: readonly Role[]
  children: ReactNode
}

export function RequireAuth({ allow, children }: RequireAuthProps) {
  const isAuthenticated = useAuth(selectIsAuthenticated)
  const role = useAuth(selectRole)
  const location = useLocation()

  if (!isAuthenticated) {
    // Remember where they were headed so login can return them there instead of dumping
    // them on a generic home page.
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  }

  if (allow && role && !allow.includes(role)) {
    // Wrong role: send them to their own area rather than showing a dead end. `replace`
    // keeps the forbidden URL out of history, so Back does not bounce them here again.
    return <Navigate to={homePathFor(role)} replace />
  }

  return <>{children}</>
}

/** Keeps a signed-in user off /login and /register by redirecting to their own home. */
export function RedirectIfAuthenticated({ children }: { children: ReactNode }) {
  const isAuthenticated = useAuth(selectIsAuthenticated)
  const role = useAuth(selectRole)

  if (isAuthenticated) return <Navigate to={homePathFor(role)} replace />
  return <>{children}</>
}
