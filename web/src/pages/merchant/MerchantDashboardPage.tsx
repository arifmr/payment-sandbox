import { Link } from 'react-router-dom'
import { invoices, wallet } from '@/api/endpoints'
import { useAsync } from '@/hooks/useAsync'
import { formatDateTime, formatMoney } from '@/lib/format'
import { useAuth } from '@/store/auth'
import { Button } from '@/components/ui/Button'
import { Card, Metric } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { AsyncBoundary, EmptyState } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/**
 * Merchant overview (SRS §4.2: "Dashboard Invoice").
 *
 * Deliberately built from list endpoints rather than a merchant-scoped stats endpoint,
 * because none exists — /admin/dashboard is admin-only by design, and a merchant must not
 * see cross-merchant totals. The counts come from the `total` in each filtered list
 * response, which is one cheap query per tile rather than fetching every invoice and
 * counting client-side.
 */
export function MerchantDashboardPage() {
  const user = useAuth((s) => s.user)

  const balance = useAsync((signal) => wallet.get(signal), [])
  // page_size 1: only the `total` is needed, so there is no reason to transfer 20 rows.
  const allCount = useAsync((signal) => invoices.list({ page_size: 1 }, signal), [])
  const paidCount = useAsync((signal) => invoices.list({ status: 'PAID', page_size: 1 }, signal), [])
  const pendingCount = useAsync(
    (signal) => invoices.list({ status: 'PENDING', page_size: 1 }, signal),
    [],
  )
  const recent = useAsync((signal) => invoices.list({ page_size: 5 }, signal), [])

  return (
    <div className="stack">
      <PageHeader
        title={`Halo, ${user?.name ?? 'Merchant'}`}
        description="Ringkasan invoice dan saldo wallet Anda."
        actions={
          <Link to="/merchant/invoices/new">
            <Button>Buat Invoice</Button>
          </Link>
        }
      />

      <div className="grid-metrics">
        <Metric
          label="Saldo Wallet"
          value={balance.isLoading ? '…' : balance.data ? formatMoney(balance.data.balance) : '—'}
        />
        <Metric
          label="Total Invoice"
          value={allCount.isLoading ? '…' : (allCount.data?.pagination.total ?? '—')}
        />
        <Metric
          label="Sudah Dibayar"
          value={paidCount.isLoading ? '…' : (paidCount.data?.pagination.total ?? '—')}
          tone="success"
        />
        <Metric
          label="Menunggu Pembayaran"
          value={pendingCount.isLoading ? '…' : (pendingCount.data?.pagination.total ?? '—')}
          tone="warning"
        />
      </div>

      <Card
        title="Invoice Terbaru"
        actions={<Link to="/merchant/invoices">Lihat semua</Link>}
        flush
      >
        <AsyncBoundary
          isLoading={recent.isLoading}
          error={recent.error}
          isEmpty={recent.status === 'success' && recent.data.data.length === 0}
          onRetry={recent.reload}
          loadingLabel="Memuat invoice terbaru"
          empty={
            <EmptyState
              title="Belum ada invoice"
              description="Buat invoice pertama untuk mulai menerima pembayaran."
              action={
                <Link to="/merchant/invoices/new">
                  <Button>Buat Invoice</Button>
                </Link>
              }
            />
          }
        >
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>No. Invoice</Th>
                  <Th>Pelanggan</Th>
                  <Th numeric>Nominal</Th>
                  <Th>Status</Th>
                  <Th>Dibuat</Th>
                  <Th>Aksi</Th>
                </tr>
              </thead>
              <tbody>
                {recent.data?.data.map((inv) => (
                  <tr key={inv.id}>
                    <Td>
                      <span className="mono">{inv.invoice_number}</span>
                    </Td>
                    <Td>{inv.customer_name}</Td>
                    <Td numeric>{formatMoney(inv.amount)}</Td>
                    <Td>
                      <StatusBadge status={inv.status} />
                    </Td>
                    <Td>{formatDateTime(inv.created_at)}</Td>
                    <Td>
                      <Link to={`/merchant/invoices/${inv.id}`}>Detail</Link>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </TableWrap>
        </AsyncBoundary>
      </Card>
    </div>
  )
}
