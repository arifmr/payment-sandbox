import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { pay } from '@/api/endpoints'
import { PAYMENT_METHODS, type PaymentIntent, type PaymentMethod, type PublicInvoice } from '@/api/types'
import { useAction, useAsync } from '@/hooks/useAsync'
import { formatDateTime, formatMoney, methodLabel, statusLabel } from '@/lib/format'
import { clearPendingIntent, getPendingIntent, setPendingIntent } from '@/lib/pendingIntent'
import { Button } from '@/components/ui/Button'
import { StatusBadge } from '@/components/ui/Badge'
import { DescriptionItem, DescriptionList } from '@/components/ui/DescriptionList'
import { Alert, AsyncBoundary, Spinner, errorMessage } from '@/components/ui/StateView'
import './PaymentPage.css'

/**
 * Public payment page (SRS §2.4 / §4.3).
 *
 * No authentication: possession of the payment token is the authorisation. It is
 * deliberately standalone — no sidebar, no app chrome — because the audience is a customer
 * who has never seen this system and only needs to answer one question: how do I pay.
 */
export function PaymentPage() {
  const { token = '' } = useParams<{ token: string }>()
  const invoice = useAsync((signal) => pay.getInvoice(token, signal), [token])
  const [intent, setIntent] = useState<PaymentIntent | null>(null)

  // Recovers the in-flight intent after a refresh. Without this, `intent` above resets to
  // null on every mount and the page falls back to the method picker — inviting a second
  // payment attempt for something that is already PENDING and simply waiting on an admin
  // to settle it. `restoring` gates rendering so the picker never flashes before this
  // lookup resolves; see lib/pendingIntent.ts for why the id has to be persisted at all.
  const [restoring, setRestoring] = useState(true)

  useEffect(() => {
    setIntent(null)
    const pendingId = getPendingIntent(token)
    if (!pendingId) {
      setRestoring(false)
      return
    }

    let cancelled = false
    pay
      .getIntent(token, pendingId)
      .then((fresh) => {
        if (!cancelled) setIntent(fresh)
      })
      .catch(() => {
        // The stored id no longer resolves to anything usable — stale after a very old
        // visit, or the intent belongs to a token that has since changed. A payer must
        // not get stuck on a page that can never confirm, so drop it and let them start
        // a fresh payment instead.
        if (!cancelled) clearPendingIntent(token)
      })
      .finally(() => {
        if (!cancelled) setRestoring(false)
      })

    return () => {
      cancelled = true
    }
  }, [token])

  const handleCreated = (created: PaymentIntent) => {
    setPendingIntent(token, created.id)
    setIntent(created)
  }

  const handleReset = () => {
    clearPendingIntent(token)
    setIntent(null)
  }

  return (
    <div className="pay-page">
      <div className="pay-card">
        <header className="pay-head">
          <p className="pay-brand text-xs">PAYMENT SANDBOX</p>
          <h1 className="pay-title">Pembayaran Invoice</h1>
        </header>

        <AsyncBoundary
          isLoading={invoice.isLoading || restoring}
          error={invoice.error}
          onRetry={invoice.reload}
          loadingLabel="Memuat detail invoice"
        >
          {invoice.data && (
            <>
              <InvoiceSummary invoice={invoice.data} />
              {intent ? (
                <PaymentStatus token={token} intent={intent} onReset={handleReset} />
              ) : (
                <MethodPicker
                  invoice={invoice.data}
                  token={token}
                  onCreated={handleCreated}
                />
              )}
            </>
          )}
        </AsyncBoundary>

        <footer className="pay-foot text-xs muted">
          Ini adalah lingkungan simulasi. Tidak ada dana nyata yang berpindah.
        </footer>
      </div>
    </div>
  )
}

function InvoiceSummary({ invoice }: { invoice: PublicInvoice }) {
  return (
    <section className="pay-summary">
      <div className="pay-amount-block">
        <span className="pay-amount-label text-xs">Total Tagihan</span>
        <strong className="pay-amount tabular">{formatMoney(invoice.amount)}</strong>
        <StatusBadge status={invoice.status} />
      </div>

      <DescriptionList>
        <DescriptionItem label="Penerima">{invoice.merchant_name}</DescriptionItem>
        <DescriptionItem label="No. Invoice">
          <span className="mono">{invoice.invoice_number}</span>
        </DescriptionItem>
        <DescriptionItem label="Atas Nama">{invoice.customer_name}</DescriptionItem>
        <DescriptionItem label="Jatuh Tempo">{formatDateTime(invoice.due_date)}</DescriptionItem>
        {invoice.description && (
          <DescriptionItem label="Keterangan" wide>
            {invoice.description}
          </DescriptionItem>
        )}
      </DescriptionList>
    </section>
  )
}

/** Method selection (SRS §2.4: WALLET / VA_DUMMY / EWALLET_DUMMY). */
function MethodPicker({
  invoice,
  token,
  onCreated,
}: {
  invoice: PublicInvoice
  token: string
  onCreated: (intent: PaymentIntent) => void
}) {
  const [method, setMethod] = useState<PaymentMethod>('VA_DUMMY')

  const action = useAction(async () => {
    const created = await pay.createIntent(token, method)
    onCreated(created)
    return created
  })

  // An invoice that is already settled or expired cannot be paid. The backend rejects it
  // too, but showing a payment form that is guaranteed to fail wastes the customer's time.
  if (invoice.status !== 'PENDING') {
    return (
      <Alert tone={invoice.status === 'PAID' ? 'success' : 'warning'}>
        {invoice.status === 'PAID'
          ? 'Invoice ini sudah dibayar. Tidak ada tindakan lain yang diperlukan.'
          : 'Invoice ini sudah kedaluwarsa dan tidak dapat dibayar. Hubungi penerima untuk tagihan baru.'}
      </Alert>
    )
  }

  return (
    <section className="stack">
      <h2 className="pay-section-title">Pilih Metode Pembayaran</h2>

      {action.error && <Alert>{errorMessage(action.error)}</Alert>}

      {/* A radiogroup rather than a <select>: three options are better shown than hidden
          behind a dropdown, and radios are what assistive tech expects for this. */}
      <div className="method-list" role="radiogroup" aria-label="Metode pembayaran">
        {PAYMENT_METHODS.map((m) => (
          <label key={m} className={m === method ? 'method is-selected' : 'method'}>
            <input
              type="radio"
              name="method"
              value={m}
              checked={m === method}
              onChange={() => setMethod(m)}
            />
            <span className="method-body">
              <span className="method-name">{methodLabel(m)}</span>
              <span className="method-hint text-xs muted">{methodHint(m)}</span>
            </span>
          </label>
        ))}
      </div>

      <Button fullWidth loading={action.isPending} onClick={() => void action.run()}>
        Bayar {formatMoney(invoice.amount)}
      </Button>
    </section>
  )
}

function methodHint(method: PaymentMethod): string {
  switch (method) {
    case 'WALLET':
      return 'Saldo wallet Anda akan dipotong jika Anda sedang masuk.'
    case 'VA_DUMMY':
      return 'Simulasi transfer virtual account.'
    case 'EWALLET_DUMMY':
      return 'Simulasi pembayaran e-wallet.'
  }
}

/** How long to keep polling before telling the user to check back later. */
const POLL_INTERVAL_MS = 3000
const POLL_TIMEOUT_MS = 2 * 60 * 1000

/**
 * Payment status with polling (SRS §4.3: "Menampilkan status pembayaran").
 *
 * A PENDING intent is settled by an admin out-of-band, so the page polls. Three details
 * make this safe rather than a runaway loop:
 *  - polling stops as soon as the intent reaches a terminal state
 *  - it gives up after a bounded time instead of hammering forever on a tab left open
 *  - the timer is cleared on unmount, so navigating away does not leak an interval
 */
function PaymentStatus({
  token,
  intent,
  onReset,
}: {
  token: string
  intent: PaymentIntent
  onReset: () => void
}) {
  const [current, setCurrent] = useState(intent)
  const [gaveUp, setGaveUp] = useState(false)
  const startedAt = useRef(Date.now())

  const isPending = current.status === 'PENDING'

  useEffect(() => {
    if (!isPending || gaveUp) return

    let cancelled = false
    const timer = setInterval(() => {
      if (Date.now() - startedAt.current > POLL_TIMEOUT_MS) {
        setGaveUp(true)
        return
      }
      pay
        .getIntent(token, current.id)
        .then((fresh) => {
          if (!cancelled) setCurrent(fresh)
        })
        // A transient failure must not kill polling — the next tick tries again.
        .catch(() => undefined)
    }, POLL_INTERVAL_MS)

    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [isPending, gaveUp, token, current.id])

  return (
    <section className="stack">
      <h2 className="pay-section-title">Status Pembayaran</h2>

      <div className={`pay-status pay-status-${current.status.toLowerCase()}`}>
        <div className="pay-status-head">
          <StatusBadge status={current.status} />
          {isPending && !gaveUp && <Spinner label="Menunggu konfirmasi" />}
        </div>
        <p className="pay-status-text">{statusText(current.status)}</p>
      </div>

      <DescriptionList>
        <DescriptionItem label="Metode">{methodLabel(current.method)}</DescriptionItem>
        <DescriptionItem label="Nominal">{formatMoney(current.amount)}</DescriptionItem>
        <DescriptionItem label="ID Pembayaran">
          <span className="mono">{current.id}</span>
        </DescriptionItem>
        <DescriptionItem label="Diproses">{formatDateTime(current.processed_at)}</DescriptionItem>
      </DescriptionList>

      {gaveUp && isPending && (
        <Alert tone="info">
          Status belum berubah setelah beberapa saat. Simpan ID pembayaran di atas dan periksa
          kembali nanti.
        </Alert>
      )}

      {current.status === 'FAILED' && (
        <Button variant="secondary" fullWidth onClick={onReset}>
          Coba metode pembayaran lain
        </Button>
      )}
    </section>
  )
}

function statusText(status: PaymentIntent['status']): string {
  switch (status) {
    case 'PENDING':
      return 'Pembayaran Anda sedang diverifikasi. Halaman ini akan diperbarui otomatis.'
    case 'SUCCESS':
      return 'Pembayaran berhasil. Invoice telah ditandai lunas.'
    case 'FAILED':
      return 'Pembayaran gagal diproses. Anda dapat mencoba metode lain.'
    default:
      return statusLabel(status)
  }
}
