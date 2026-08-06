import type { ReactNode } from 'react'
import { ApiError, NetworkError } from '@/api/client'
import { Button } from './Button'
import './StateView.css'

/**
 * The loading / error / empty states required by SRS §4.1, in one place.
 *
 * Every list and detail view routes through this, which is what makes the three states
 * consistent instead of each page inventing its own spinner and its own idea of what an
 * error looks like. The alternative — inline `{loading && <p>Loading…</p>}` — is how
 * pages end up silently rendering nothing on failure.
 */

export function Spinner({ label = 'Memuat' }: { label?: string }) {
  return (
    <span className="spinner" role="status" aria-live="polite">
      <span className="spinner-ring" aria-hidden="true" />
      <span className="sr-only">{label}…</span>
    </span>
  )
}

export function LoadingState({ label = 'Memuat data' }: { label?: string }) {
  return (
    <div className="state-view" role="status" aria-live="polite">
      <span className="spinner-ring spinner-lg" aria-hidden="true" />
      <p className="state-title">{label}…</p>
    </div>
  )
}

export function EmptyState({
  title = 'Belum ada data',
  description,
  action,
}: {
  title?: string
  description?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="state-view">
      <div className="state-icon" aria-hidden="true">
        ∅
      </div>
      <p className="state-title">{title}</p>
      {description && <p className="state-desc">{description}</p>}
      {action && <div className="state-action">{action}</div>}
    </div>
  )
}

/**
 * Turns an error into something a user can act on.
 *
 * A domain error from the backend already carries a message written for humans
 * (`INVOICE_NOT_PAID` → "only PAID invoices can be refunded"), so it is shown as-is. An
 * unclassified 500 is not: its message is deliberately generic, so a fixed line is used
 * instead of exposing whatever leaked through.
 */
export function errorMessage(error: Error): string {
  if (error instanceof NetworkError) return error.message
  if (error instanceof ApiError) {
    if (error.status >= 500) return 'Server sedang bermasalah. Silakan coba lagi sebentar lagi.'
    return error.message
  }
  return 'Terjadi kesalahan yang tidak terduga.'
}

/** The error code, shown small so a user can quote it in a bug report. */
function errorCode(error: Error): string | null {
  return error instanceof ApiError ? error.code : null
}

export function ErrorState({ error, onRetry }: { error: Error; onRetry?: () => void }) {
  const code = errorCode(error)
  return (
    <div className="state-view state-error" role="alert">
      <div className="state-icon" aria-hidden="true">
        !
      </div>
      <p className="state-title">Gagal memuat data</p>
      <p className="state-desc">{errorMessage(error)}</p>
      {code && <p className="state-code mono">{code}</p>}
      {onRetry && (
        <div className="state-action">
          <Button variant="secondary" onClick={onRetry}>
            Coba lagi
          </Button>
        </div>
      )}
    </div>
  )
}

/** Inline error for forms and actions, where a full-panel state would be too heavy. */
export function Alert({
  tone = 'danger',
  children,
}: {
  tone?: 'danger' | 'success' | 'warning' | 'info'
  children: ReactNode
}) {
  return (
    <div
      className={`alert alert-${tone}`}
      // Failures must be announced immediately; a success confirmation can wait for a
      // natural pause, so it uses the politer live region.
      role={tone === 'danger' ? 'alert' : 'status'}
    >
      {children}
    </div>
  )
}

interface AsyncBoundaryProps {
  isLoading: boolean
  error: Error | null
  isEmpty?: boolean
  onRetry?: () => void
  loadingLabel?: string
  empty?: ReactNode
  children: ReactNode
}

/**
 * Renders exactly one of loading / error / empty / content.
 *
 * The order matters and is deliberate: loading wins over empty, because a list is also
 * "empty" while it is still being fetched and flashing "no data" mid-request is worse
 * than showing nothing.
 */
export function AsyncBoundary({
  isLoading,
  error,
  isEmpty = false,
  onRetry,
  loadingLabel,
  empty,
  children,
}: AsyncBoundaryProps) {
  if (isLoading) return <LoadingState {...(loadingLabel ? { label: loadingLabel } : {})} />
  if (error) return <ErrorState error={error} {...(onRetry ? { onRetry } : {})} />
  if (isEmpty) return <>{empty ?? <EmptyState />}</>
  return <>{children}</>
}
