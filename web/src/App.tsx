import { Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout/AppShell'
import { RedirectIfAuthenticated, RequireAuth } from '@/components/layout/RequireAuth'
import { homePathFor, selectRole, useAuth } from '@/store/auth'
import { LoginPage } from '@/pages/LoginPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { PaymentPage } from '@/pages/pay/PaymentPage'
import { MerchantDashboardPage } from '@/pages/merchant/MerchantDashboardPage'
import { InvoiceListPage } from '@/pages/merchant/InvoiceListPage'
import { InvoiceCreatePage } from '@/pages/merchant/InvoiceCreatePage'
import { InvoiceDetailPage } from '@/pages/merchant/InvoiceDetailPage'
import { WalletPage } from '@/pages/merchant/WalletPage'
import { RefundsPage } from '@/pages/merchant/RefundsPage'
import { AdminDashboardPage } from '@/pages/admin/AdminDashboardPage'
import { AdminPaymentsPage } from '@/pages/admin/AdminPaymentsPage'
import { AdminRefundsPage } from '@/pages/admin/AdminRefundsPage'
import { AdminTopupsPage } from '@/pages/admin/AdminTopupsPage'

/**
 * Route table.
 *
 * Three access tiers, matching the backend's own grouping:
 *  - public:    /login, /register, /pay/:token
 *  - merchant:  /merchant/*
 *  - admin:     /admin/*
 *
 * The payment page is public on purpose — a customer paying an invoice has no account, and
 * requiring one would make the payment link useless. Possession of the token is the
 * authorisation, exactly as on the API side.
 */
export function App() {
  return (
    <Routes>
      {/* Public */}
      <Route
        path="/login"
        element={
          <RedirectIfAuthenticated>
            <LoginPage />
          </RedirectIfAuthenticated>
        }
      />
      <Route
        path="/register"
        element={
          <RedirectIfAuthenticated>
            <RegisterPage />
          </RedirectIfAuthenticated>
        }
      />
      <Route path="/pay/:token" element={<PaymentPage />} />

      {/* Merchant */}
      <Route
        path="/merchant"
        element={
          <RequireAuth allow={['MERCHANT']}>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route index element={<MerchantDashboardPage />} />
        <Route path="invoices" element={<InvoiceListPage />} />
        {/* 'new' is declared before ':id' so it is not swallowed as an invoice id. */}
        <Route path="invoices/new" element={<InvoiceCreatePage />} />
        <Route path="invoices/:id" element={<InvoiceDetailPage />} />
        <Route path="wallet" element={<WalletPage />} />
        <Route path="refunds" element={<RefundsPage />} />
      </Route>

      {/* Admin */}
      <Route
        path="/admin"
        element={
          <RequireAuth allow={['ADMIN']}>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route index element={<AdminDashboardPage />} />
        <Route path="payments" element={<AdminPaymentsPage />} />
        <Route path="refunds" element={<AdminRefundsPage />} />
        <Route path="topups" element={<AdminTopupsPage />} />
      </Route>

      <Route path="/" element={<RootRedirect />} />
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  )
}

/** Sends '/' to the right place for whoever is looking at it. */
function RootRedirect() {
  const role = useAuth(selectRole)
  const isAuthenticated = useAuth((s) => Boolean(s.accessToken && s.user))
  return <Navigate to={isAuthenticated ? homePathFor(role) : '/login'} replace />
}
