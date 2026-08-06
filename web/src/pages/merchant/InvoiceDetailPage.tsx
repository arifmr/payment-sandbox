import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { invoices, refunds } from '@/api/endpoints'
import { useAction, useAsync } from '@/hooks/useAsync'
import { formatDateTime, formatMoney } from '@/lib/format'
import { validateAmount } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { CopyField } from '@/components/ui/CopyField'
import { DescriptionItem, DescriptionList } from '@/components/ui/DescriptionList'
import { TextAreaField, TextField } from '@/components/ui/Field'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { Alert, AsyncBoundary, errorMessage } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'
import type { Invoice, Refund } from '@/api/types'

/**
 * Invoice detail with payment link and refund request (SRS §4.2).
 *
 * The refund section only appears for a PAID invoice, mirroring the backend rule that only
 * a PAID invoice can be refunded. Hiding an action the server would reject is better than
 * offering it and explaining the 422 afterwards.
 */
export function InvoiceDetailPage() {
  const { id = '' } = useParams<{ id: string }>()

  const state = useAsync((signal) => invoices.get(id, signal), [id])

  return (
    <div className="stack">
      <PageHeader
        title="Detail Invoice"
        description={<Link to="/merchant/invoices">← Kembali ke daftar invoice</Link>}
      />

      <AsyncBoundary
        isLoading={state.isLoading}
        error={state.error}
        onRetry={state.reload}
        loadingLabel="Memuat invoice"
      >
        {state.data && <InvoiceDetail invoice={state.data} onChanged={state.reload} />}
      </AsyncBoundary>
    </div>
  )
}

function InvoiceDetail({ invoice, onChanged }: { invoice: Invoice; onChanged: () => void }) {
  // The API returns a path (/api/v1/pay/<token>); make it a full URL so the merchant can
  // paste it anywhere, and so "Buka" works.
  const paymentUrl = buildPaymentUrl(invoice)

  return (
    <>
      <Card
        title={invoice.invoice_number}
        description={`Dibuat ${formatDateTime(invoice.created_at)}`}
        actions={<StatusBadge status={invoice.status} />}
      >
        <DescriptionList>
          <DescriptionItem label="Nominal">
            <strong>{formatMoney(invoice.amount)}</strong>
          </DescriptionItem>
          <DescriptionItem label="Pelanggan">{invoice.customer_name}</DescriptionItem>
          <DescriptionItem label="Email Pelanggan">
            {invoice.customer_email || <span className="muted">—</span>}
          </DescriptionItem>
          <DescriptionItem label="Jatuh Tempo">{formatDateTime(invoice.due_date)}</DescriptionItem>
          <DescriptionItem label="Dibayar Pada">
            {invoice.paid_at ? formatDateTime(invoice.paid_at) : <span className="muted">—</span>}
          </DescriptionItem>
          {invoice.description && (
            <DescriptionItem label="Deskripsi" wide>
              {invoice.description}
            </DescriptionItem>
          )}
        </DescriptionList>
      </Card>

      <Card
        title="Tautan Pembayaran"
        description="Bagikan tautan ini kepada pelanggan. Tidak perlu login untuk membayar."
      >
        <div className="stack">
          <CopyField label="Payment link" value={paymentUrl} href={paymentUrl} />
          {invoice.status !== 'PENDING' && (
            <Alert tone="info">
              Invoice ini sudah <strong>{invoice.status === 'PAID' ? 'dibayar' : 'kedaluwarsa'}</strong>,
              sehingga tautan tidak dapat digunakan lagi untuk membayar.
            </Alert>
          )}
        </div>
      </Card>

      {invoice.status === 'PAID' && <RefundSection invoice={invoice} onChanged={onChanged} />}
    </>
  )
}

/**
 * Refund request + status (SRS §2.5 / §4.2).
 *
 * The backend lets a merchant hold more than one in-flight refund claim against an invoice,
 * as long as their cumulative total stays inside the invoice amount
 * (agent_documentation/02-data-integrity.md §5). The UI narrows that on purpose: once a
 * refund is REQUESTED or APPROVED, the request form is replaced by that refund's status
 * instead of offering another submission. Two things this fixes, both reported directly:
 *
 *  - previously the form kept showing even with a refund already pending, inviting a
 *    second submission for the same invoice;
 *  - previously "already submitted" lived only in this component's local state, so it
 *    reappeared on every reload of the page — the very next thing a merchant is likely to
 *    do after submitting.
 *
 * Which refunds belong to this invoice is worked out client-side: `GET /refunds` has no
 * `invoice_id` filter (only the admin endpoints do), so the merchant's own refunds are
 * fetched and filtered down to this invoice. page_size is set to the documented maximum
 * (100) to keep this correct for the common case; a merchant with more than 100 refunds
 * across *all* their invoices could have an older one fall off this page — a known
 * limitation of doing the filter here rather than adding a query param on the backend.
 */
function RefundSection({ invoice, onChanged }: { invoice: Invoice; onChanged: () => void }) {
  const list = useAsync((signal) => refunds.listMine({ page: 1, page_size: 100 }, signal), [invoice.id])

  return (
    <Card title="Refund" description="Refund memerlukan persetujuan admin sebelum saldo dipotong.">
      <AsyncBoundary
        isLoading={list.isLoading}
        error={list.error}
        onRetry={list.reload}
        loadingLabel="Memuat status refund"
      >
        {list.data && (
          <RefundSectionBody
            invoice={invoice}
            mine={list.data.data.filter((r) => r.invoice_id === invoice.id)}
            onChanged={() => {
              onChanged()
              list.reload()
            }}
          />
        )}
      </AsyncBoundary>
    </Card>
  )
}

function RefundSectionBody({
  invoice,
  mine,
  onChanged,
}: {
  invoice: Invoice
  mine: Refund[]
  onChanged: () => void
}) {
  // REQUESTED and APPROVED still hold a claim on the invoice (02-data-integrity.md §5).
  // REJECTED and FAILED release it, and SUCCESS is a completed payout — in every one of
  // those three cases whether a further request fits is something only the server's
  // cumulative check can decide, so the form comes back rather than staying hidden forever.
  const active = mine.find((r) => r.status === 'REQUESTED' || r.status === 'APPROVED')

  return (
    <div className="stack">
      {mine.length > 0 && <RefundHistory refunds={mine} />}

      {active ? (
        <Alert tone="info">
          Refund sebesar <strong>{formatMoney(active.amount)}</strong>{' '}
          {active.status === 'REQUESTED'
            ? 'sedang menunggu persetujuan admin.'
            : 'sudah disetujui dan menunggu diproses admin.'}{' '}
          Pengajuan baru dapat dilakukan setelah refund ini selesai diproses.
        </Alert>
      ) : (
        <RefundRequestForm invoice={invoice} onCreated={onChanged} />
      )}
    </div>
  )
}

/** Every refund this invoice has ever had, newest first — the "keterangan refund" a merchant reads to see what already happened, not only what is currently pending. */
function RefundHistory({ refunds: items }: { refunds: Refund[] }) {
  const sorted = [...items].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  )
  return (
    <TableWrap>
      <Table>
        <thead>
          <tr>
            <Th>Diajukan</Th>
            <Th numeric>Nominal</Th>
            <Th>Alasan</Th>
            <Th>Status</Th>
            <Th>Diproses</Th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => (
            <tr key={r.id}>
              <Td>{formatDateTime(r.created_at)}</Td>
              <Td numeric>{formatMoney(r.amount)}</Td>
              <Td>{r.reason || <span className="muted">—</span>}</Td>
              <Td>
                <StatusBadge status={r.status} />
              </Td>
              <Td>{formatDateTime(r.processed_at)}</Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </TableWrap>
  )
}

function RefundRequestForm({ invoice, onCreated }: { invoice: Invoice; onCreated: () => void }) {
  const [amount, setAmount] = useState(String(invoice.amount))
  const [reason, setReason] = useState('')
  const [amountError, setAmountError] = useState<string | undefined>()

  const action = useAction(async () => {
    const refund = await refunds.request({
      invoice_id: invoice.id,
      amount: Number(amount),
      ...(reason.trim() ? { reason: reason.trim() } : {}),
    })
    onCreated()
    return refund
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    // The invoice total is an upper bound the client can check. It is not the real limit:
    // the backend caps the cumulative total across every in-flight and settled refund,
    // which only the server can compute. So a valid-looking amount can still be rejected,
    // and that rejection is surfaced as-is.
    const err = validateAmount(amount, { max: invoice.amount })
    setAmountError(err)
    if (err) return
    void action.run()
  }

  return (
    <form className="stack" onSubmit={handleSubmit} noValidate>
      {action.error && <Alert>{errorMessage(action.error)}</Alert>}

      <TextField
        label="Nominal Refund (IDR)"
        type="number"
        inputMode="numeric"
        min={1}
        max={invoice.amount}
        step={1}
        value={amount}
        error={amountError}
        required
        hint={`Maksimal ${formatMoney(invoice.amount)}. Refund sebagian diperbolehkan.`}
        onChange={(e) => {
          setAmount(e.target.value)
          if (amountError) setAmountError(undefined)
          if (action.error) action.reset()
        }}
      />

      <TextAreaField
        label="Alasan"
        value={reason}
        hint="Opsional, maksimal 500 karakter."
        maxLength={500}
        placeholder="Pelanggan membatalkan pesanan"
        onChange={(e) => setReason(e.target.value)}
      />

      <div className="row">
        <Button type="submit" variant="danger" loading={action.isPending}>
          Ajukan Refund
        </Button>
      </div>
    </form>
  )
}

/**
 * Turns the API's payment path into an absolute URL pointing at *this* app's payment page.
 *
 * The backend returns `/api/v1/pay/<token>`, which is the API route, not a page a customer
 * should open. The token is what matters, so it is lifted out and used to build the SPA
 * route instead.
 */
export function buildPaymentUrl(
  invoice: Pick<Invoice, 'payment_token' | 'payment_link'>,
  origin: string = window.location.origin,
): string {
  const token =
    invoice.payment_token || invoice.payment_link?.split('/').filter(Boolean).pop() || ''
  return `${origin}/pay/${token}`
}
