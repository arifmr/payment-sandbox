import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth, selectRole } from '@/store/auth'
import { Button } from '@/components/ui/Button'
import './AppShell.css'

/**
 * Authenticated layout: sidebar navigation, top bar, routed content.
 *
 * Responsive strategy (SRS §4.1): the sidebar is a fixed column on wide screens and an
 * off-canvas drawer below 900px. The drawer is driven by React state rather than a CSS
 * `:target` trick because it needs to close on navigation, and closing on route change is
 * the difference between a usable mobile nav and one that covers the page you just opened.
 */

interface NavItem {
  to: string
  label: string
  /** Exact matching for index routes, so /merchant does not stay active on every child. */
  end?: boolean
}

const MERCHANT_NAV: NavItem[] = [
  { to: '/merchant', label: 'Ringkasan', end: true },
  { to: '/merchant/invoices', label: 'Invoice' },
  { to: '/merchant/invoices/new', label: 'Buat Invoice' },
  { to: '/merchant/wallet', label: 'Wallet & Top-up' },
  { to: '/merchant/refunds', label: 'Refund' },
]

const ADMIN_NAV: NavItem[] = [
  { to: '/admin', label: 'Dashboard', end: true },
  { to: '/admin/payments', label: 'Simulasi Pembayaran' },
  { to: '/admin/refunds', label: 'Kelola Refund' },
  { to: '/admin/topups', label: 'Kelola Top-up' },
]

export function AppShell() {
  const role = useAuth(selectRole)
  const user = useAuth((s) => s.user)
  const logout = useAuth((s) => s.logout)
  const busy = useAuth((s) => s.busy)

  const navigate = useNavigate()
  const location = useLocation()
  const [drawerOpen, setDrawerOpen] = useState(false)

  // Close the drawer whenever the route changes, so it never hides the page it opened.
  useEffect(() => setDrawerOpen(false), [location.pathname])

  // Escape closes the drawer — expected of any overlay, and the only way out for a
  // keyboard user if the backdrop is not reachable.
  useEffect(() => {
    if (!drawerOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDrawerOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [drawerOpen])

  const items = role === 'ADMIN' ? ADMIN_NAV : MERCHANT_NAV

  async function handleLogout() {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="shell">
      {/* Lets keyboard users jump past the nav on every page. */}
      <a className="skip-link" href="#main">
        Lewati ke konten utama
      </a>

      <header className="topbar">
        <div className="topbar-left">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setDrawerOpen((v) => !v)}
            aria-expanded={drawerOpen}
            aria-controls="app-sidebar"
          >
            <span aria-hidden="true">☰</span>
            <span className="sr-only">Menu navigasi</span>
          </Button>
          <Link to={role === 'ADMIN' ? '/admin' : '/merchant'} className="brand">
            Payment&nbsp;Sandbox
          </Link>
          <span className="role-chip">{role === 'ADMIN' ? 'Admin' : 'Merchant'}</span>
        </div>

        <div className="topbar-right">
          <span className="user-name text-sm">{user?.name}</span>
          <Button variant="secondary" size="sm" onClick={handleLogout} loading={busy}>
            Keluar
          </Button>
        </div>
      </header>

      <div className="shell-body">
        <nav
          id="app-sidebar"
          className={drawerOpen ? 'sidebar is-open' : 'sidebar'}
          aria-label="Navigasi utama"
        >
          <ul className="nav-list">
            {items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.end}
                  className={({ isActive }) => (isActive ? 'nav-link is-active' : 'nav-link')}
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
          <p className="sidebar-note text-xs muted">
            Semua transaksi bersifat simulasi. Tidak ada dana nyata yang berpindah.
          </p>
        </nav>

        {/* Backdrop only exists while the drawer is open, so it cannot swallow clicks. */}
        {drawerOpen && (
          <button
            className="backdrop"
            onClick={() => setDrawerOpen(false)}
            aria-label="Tutup menu navigasi"
          />
        )}

        <main id="main" className="content">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
