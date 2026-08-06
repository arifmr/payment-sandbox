import { Link } from 'react-router-dom'
import { homePathFor, selectIsAuthenticated, selectRole, useAuth } from '@/store/auth'
import { Button } from '@/components/ui/Button'
import './NotFoundPage.css'

/**
 * 404. Offers a way back that depends on who is looking: sending a logged-out visitor to
 * /merchant would just bounce them through the guard to /login.
 */
export function NotFoundPage() {
  const isAuthenticated = useAuth(selectIsAuthenticated)
  const role = useAuth(selectRole)
  const target = isAuthenticated ? homePathFor(role) : '/login'

  return (
    <div className="notfound">
      <p className="notfound-code">404</p>
      <h1 className="notfound-title">Halaman tidak ditemukan</h1>
      <p className="muted text-sm">Tautan mungkin salah atau sudah tidak berlaku.</p>
      <Link to={target}>
        <Button>{isAuthenticated ? 'Kembali ke dashboard' : 'Ke halaman masuk'}</Button>
      </Link>
    </div>
  )
}
