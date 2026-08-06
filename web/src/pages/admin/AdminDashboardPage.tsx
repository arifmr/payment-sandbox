import { useState } from 'react'
import { admin } from '@/api/endpoints'
import { useAsync } from '@/hooks/useAsync'
import { formatAmount, formatMoney } from '@/lib/format'
import { validateUUID } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { Card, Metric } from '@/components/ui/Card'
import { Alert, AsyncBoundary } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/** Admin statistics with merchant and date filters (SRS §2.6 / §4.4). */
export function AdminDashboardPage() {
  const [merchantId, setMerchantId] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [merchantIdError, setMerchantIdError] = useState<string | undefined>()

  // Filters are applied on submit, not per keystroke: a half-typed UUID would otherwise
  // fire a request per character and each would come back 400.
  const [applied, setApplied] = useState<{ merchant_id?: string; from?: string; to?: string }>({})

  const stats = useAsync((signal) => admin.dashboard(applied, signal), [JSON.stringify(applied)])

  function apply(e: React.FormEvent) {
    e.preventDefault()
    const id = merchantId.trim()
    if (id) {
      const err = validateUUID(id, 'Merchant ID')
      setMerchantIdError(err)
      if (err) return
    } else {
      setMerchantIdError(undefined)
    }

    setApplied({
      ...(id ? { merchant_id: id } : {}),
      ...(from ? { from: new Date(`${from}T00:00:00`).toISOString() } : {}),
      ...(to ? { to: new Date(`${to}T23:59:59`).toISOString() } : {}),
    })
  }

  function reset() {
    setMerchantId('')
    setFrom('')
    setTo('')
    setMerchantIdError(undefined)
    setApplied({})
  }

  const hasFilter = Object.keys(applied).length > 0

  return (
    <div className="stack">
      <PageHeader
        title="Dashboard"
        description="Statistik transaksi seluruh merchant."
        actions={
          <Button variant="ghost" size="sm" onClick={stats.reload} disabled={stats.isLoading}>
            Muat ulang
          </Button>
        }
      />

      <Card
        title="Filter"
        description={hasFilter ? 'Statistik di bawah sudah difilter.' : 'Menampilkan seluruh data.'}
        actions={
          hasFilter && (
            <Button variant="ghost" size="sm" onClick={reset}>
              Reset filter
            </Button>
          )
        }
      >
        <form className="stack" onSubmit={apply}>
          <div className="grid-2">
            <div className="filter">
              <label className="filter-label" htmlFor="dash-merchant">
                Merchant ID (UUID)
              </label>
              <input
                id="dash-merchant"
                className="field-control"
                value={merchantId}
                placeholder="Kosongkan untuk semua merchant"
                aria-invalid={merchantIdError ? true : undefined}
                onChange={(e) => {
                  setMerchantId(e.target.value)
                  if (merchantIdError) setMerchantIdError(undefined)
                }}
              />
              {merchantIdError && (
                <p className="field-error" role="alert">
                  {merchantIdError}
                </p>
              )}
            </div>

            <label className="filter">
              <span className="filter-label">Dari tanggal</span>
              <input
                className="field-control"
                type="date"
                value={from}
                max={to || undefined}
                onChange={(e) => setFrom(e.target.value)}
              />
            </label>

            <label className="filter">
              <span className="filter-label">Sampai tanggal</span>
              <input
                className="field-control"
                type="date"
                value={to}
                min={from || undefined}
                onChange={(e) => setTo(e.target.value)}
              />
            </label>
          </div>

          <div className="row">
            <Button type="submit">Terapkan Filter</Button>
          </div>
        </form>
      </Card>

      <AsyncBoundary
        isLoading={stats.isLoading}
        error={stats.error}
        onRetry={stats.reload}
        loadingLabel="Memuat statistik"
      >
        {stats.data && (
          <>
            <div className="grid-metrics">
              <Metric label="Total Invoice" value={formatAmount(stats.data.total_invoices)} />
              <Metric
                label="Invoice Dibayar"
                value={formatAmount(stats.data.total_paid)}
                tone="success"
              />
              <Metric
                label="Pembayaran Gagal"
                value={formatAmount(stats.data.total_failed)}
                tone="danger"
                hint="Dihitung per percobaan pembayaran, bukan per invoice."
              />
              <Metric
                label="Invoice Kedaluwarsa"
                value={formatAmount(stats.data.total_expired)}
                tone="warning"
              />
            </div>

            <div className="grid-2">
              <Card title="Nominal Transaksi">
                <p className="stat-big tabular">{formatMoney(stats.data.total_amount_paid)}</p>
                <p className="muted text-sm">Total nominal invoice yang berstatus PAID.</p>
              </Card>
              <Card title="Nominal Refund">
                <p className="stat-big tabular">{formatMoney(stats.data.total_amount_refund)}</p>
                <p className="muted text-sm">
                  Hanya refund yang sudah berstatus SUCCESS (benar-benar dicairkan).
                </p>
              </Card>
            </div>

            {/* Explains a genuinely confusing aspect of the numbers rather than leaving the
                reader to wonder why the two counts do not reconcile. */}
            <Alert tone="info">
              <strong>Catatan:</strong> “Pembayaran Gagal” menghitung <em>payment intent</em> yang
              gagal, sehingga satu invoice dengan tiga percobaan gagal menyumbang 3. Angka ini
              tidak sebanding langsung dengan “Total Invoice”.
            </Alert>
          </>
        )}
      </AsyncBoundary>
    </div>
  )
}
