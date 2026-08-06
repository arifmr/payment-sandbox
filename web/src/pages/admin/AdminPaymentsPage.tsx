import { useState } from 'react'
import { admin } from '@/api/endpoints'
import { PAYMENT_STATUSES, type PaymentIntent, type PaymentIntentStatus, type SettleAction } from '@/api/types'
import { useAction } from '@/hooks/useAsync'
import { usePaginatedList } from '@/hooks/usePaginatedList'
import { formatDateTime, formatMoney, methodLabel, shortId, statusLabel } from '@/lib/format'
import { validateUUID } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { Pagination } from '@/components/ui/Pagination'
import { Alert, AsyncBoundary, EmptyState, errorMessage } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/**
 * Payment simulation panel (SRS §4.4: search a payment intent, set SUCCESS or FAILED).
 *
 * Settling a payment moves money — it credits the merchant and marks the invoice PAID — so
 * it is irreversible. The row therefore asks for confirmation before acting, rather than
 * putting a one-click money-mover in a table.
 */
export function AdminPaymentsPage() {
  const [status, setStatus] = useState<PaymentIntentStatus | ''>('PENDING')
  const [invoiceId, setInvoiceId] = useState('')
  const [invoiceIdError, setInvoiceIdError] = useState<string | undefined>()
  // Applied separately from the input so an incomplete UUID does not fire a request on
  // every keystroke and produce a stream of 400s.
  const [appliedInvoiceId, setAppliedInvoiceId] = useState('')

  const list = usePaginatedList(
    (params, signal) =>
      admin.listPayments(
        {
          ...params,
          ...(status ? { status } : {}),
          ...(appliedInvoiceId ? { invoice_id: appliedInvoiceId } : {}),
        },
        signal,
      ),
    [status, appliedInvoiceId],
  )

  function applyInvoiceFilter(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = invoiceId.trim()
    if (!trimmed) {
      setInvoiceIdError(undefined)
      setAppliedInvoiceId('')
      return
    }
    const err = validateUUID(trimmed, 'Invoice ID')
    setInvoiceIdError(err)
    if (err) return
    setAppliedInvoiceId(trimmed)
  }

  function clearFilters() {
    setStatus('')
    setInvoiceId('')
    setInvoiceIdError(undefined)
    setAppliedInvoiceId('')
  }

  return (
    <div className="stack">
      <PageHeader
        title="Simulasi Pembayaran"
        description="Cari payment intent lalu tetapkan hasilnya menjadi SUCCESS atau FAILED."
      />

      <Card
        title="Cari Payment Intent"
        actions={
          <Button variant="ghost" size="sm" onClick={clearFilters}>
            Reset filter
          </Button>
        }
      >
        <form className="grid-2" onSubmit={applyInvoiceFilter}>
          <label className="filter">
            <span className="filter-label">Status</span>
            <select
              className="field-control"
              value={status}
              onChange={(e) => setStatus(e.target.value as PaymentIntentStatus | '')}
            >
              <option value="">Semua status</option>
              {PAYMENT_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {statusLabel(s)}
                </option>
              ))}
            </select>
          </label>

          <div className="filter">
            <label className="filter-label" htmlFor="filter-invoice-id">
              Invoice ID (UUID)
            </label>
            <div className="row">
              <input
                id="filter-invoice-id"
                className="field-control"
                style={{ flex: '1 1 240px' }}
                value={invoiceId}
                placeholder="00000000-0000-0000-0000-000000000000"
                aria-invalid={invoiceIdError ? true : undefined}
                onChange={(e) => {
                  setInvoiceId(e.target.value)
                  if (invoiceIdError) setInvoiceIdError(undefined)
                }}
              />
              <Button type="submit" variant="secondary" size="sm">
                Cari
              </Button>
            </div>
            {invoiceIdError && (
              <p className="field-error" role="alert">
                {invoiceIdError}
              </p>
            )}
          </div>
        </form>
      </Card>

      <Card
        title={`Payment Intent (${list.total})`}
        actions={
          <Button variant="ghost" size="sm" onClick={list.reload} disabled={list.isLoading}>
            Muat ulang
          </Button>
        }
        flush
      >
        <AsyncBoundary
          isLoading={list.isLoading}
          error={list.error}
          isEmpty={list.isEmpty}
          onRetry={list.reload}
          loadingLabel="Memuat payment intent"
          empty={
            <EmptyState
              title="Tidak ada payment intent"
              description="Ubah filter, atau tunggu pelanggan membuat pembayaran baru."
            />
          }
        >
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Dibuat</Th>
                  <Th>ID</Th>
                  <Th>Invoice</Th>
                  <Th>Metode</Th>
                  <Th numeric>Nominal</Th>
                  <Th>Status</Th>
                  <Th>Aksi</Th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((intent) => (
                  <PaymentRow key={intent.id} intent={intent} onSettled={list.reload} />
                ))}
              </tbody>
            </Table>
          </TableWrap>

          <Pagination
            page={list.page}
            pageSize={list.pageSize}
            total={list.total}
            totalPages={list.totalPages}
            onPageChange={list.setPage}
            disabled={list.isLoading}
          />
        </AsyncBoundary>
      </Card>
    </div>
  )
}

function PaymentRow({ intent, onSettled }: { intent: PaymentIntent; onSettled: () => void }) {
  const [confirming, setConfirming] = useState<SettleAction | null>(null)

  const action = useAction(async (settleAction: SettleAction) => {
    const updated = await admin.settlePayment(intent.id, settleAction)
    setConfirming(null)
    onSettled()
    return updated
  })

  const settled = intent.status !== 'PENDING'

  return (
    <>
      <tr>
        <Td>{formatDateTime(intent.created_at)}</Td>
        <Td title={intent.id}>
          <span className="mono">{shortId(intent.id)}</span>
        </Td>
        <Td title={intent.invoice_id}>
          <span className="mono">{shortId(intent.invoice_id)}</span>
        </Td>
        <Td>{methodLabel(intent.method)}</Td>
        <Td numeric>{formatMoney(intent.amount)}</Td>
        <Td>
          <StatusBadge status={intent.status} />
        </Td>
        <Td>
          {settled ? (
            <span className="muted text-xs">Sudah final</span>
          ) : confirming ? (
            <div className="row">
              <Button
                size="sm"
                variant={confirming === 'SUCCESS' ? 'success' : 'danger'}
                loading={action.isPending}
                onClick={() => void action.run(confirming)}
              >
                Ya, {confirming === 'SUCCESS' ? 'sukseskan' : 'gagalkan'}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setConfirming(null)}
                disabled={action.isPending}
              >
                Batal
              </Button>
            </div>
          ) : (
            <div className="row">
              <Button size="sm" variant="success" onClick={() => setConfirming('SUCCESS')}>
                SUCCESS
              </Button>
              <Button size="sm" variant="danger" onClick={() => setConfirming('FAILED')}>
                FAILED
              </Button>
            </div>
          )}
        </Td>
      </tr>
      {action.error && (
        <tr>
          {/* Errors are shown in the row's own context rather than a page-level banner, so
              it is obvious which intent failed when several are being settled. */}
          <td colSpan={7}>
            <Alert>{errorMessage(action.error)}</Alert>
          </td>
        </tr>
      )}
    </>
  )
}
