import { useState } from 'react'
import { Link } from 'react-router-dom'
import { invoices } from '@/api/endpoints'
import { INVOICE_STATUSES, type InvoiceStatus } from '@/api/types'
import { usePaginatedList } from '@/hooks/usePaginatedList'
import { formatDate, formatMoney, statusLabel } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { Pagination } from '@/components/ui/Pagination'
import { AsyncBoundary, EmptyState } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/** Invoice dashboard with filters (SRS §2.3 / §4.2). */
export function InvoiceListPage() {
  const [status, setStatus] = useState<InvoiceStatus | ''>('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')

  const list = usePaginatedList(
    (params, signal) =>
      invoices.list(
        {
          ...params,
          // Empty strings are dropped by the client, so an unset filter is simply absent
          // from the query rather than sent as `status=`.
          ...(status ? { status } : {}),
          // The date input gives a calendar day; widen it to cover the whole local day so
          // "sampai hari ini" includes invoices created this afternoon.
          ...(from ? { from: new Date(`${from}T00:00:00`).toISOString() } : {}),
          ...(to ? { to: new Date(`${to}T23:59:59`).toISOString() } : {}),
        },
        signal,
      ),
    [status, from, to],
  )

  const hasFilter = Boolean(status || from || to)

  function clearFilters() {
    setStatus('')
    setFrom('')
    setTo('')
  }

  return (
    <div className="stack">
      <PageHeader
        title="Invoice"
        description="Daftar invoice beserta status pembayarannya."
        actions={
          <Link to="/merchant/invoices/new">
            <Button>Buat Invoice</Button>
          </Link>
        }
      />

      <Card
        title="Filter"
        actions={
          hasFilter && (
            <Button variant="ghost" size="sm" onClick={clearFilters}>
              Reset filter
            </Button>
          )
        }
      >
        <div className="grid-2">
          <label className="filter">
            <span className="filter-label">Status</span>
            <select
              className="field-control"
              value={status}
              onChange={(e) => setStatus(e.target.value as InvoiceStatus | '')}
            >
              <option value="">Semua status</option>
              {INVOICE_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {statusLabel(s)}
                </option>
              ))}
            </select>
          </label>

          <label className="filter">
            <span className="filter-label">Dibuat sejak</span>
            <input
              className="field-control"
              type="date"
              value={from}
              // Prevents choosing a range that cannot contain anything.
              max={to || undefined}
              onChange={(e) => setFrom(e.target.value)}
            />
          </label>

          <label className="filter">
            <span className="filter-label">Sampai</span>
            <input
              className="field-control"
              type="date"
              value={to}
              min={from || undefined}
              onChange={(e) => setTo(e.target.value)}
            />
          </label>
        </div>
      </Card>

      <Card title={`Hasil (${list.total})`} flush>
        <AsyncBoundary
          isLoading={list.isLoading}
          error={list.error}
          isEmpty={list.isEmpty}
          onRetry={list.reload}
          loadingLabel="Memuat invoice"
          empty={
            <EmptyState
              title={hasFilter ? 'Tidak ada invoice yang cocok' : 'Belum ada invoice'}
              description={
                hasFilter
                  ? 'Coba ubah atau reset filter di atas.'
                  : 'Invoice yang Anda buat akan muncul di sini.'
              }
              action={
                hasFilter ? (
                  <Button variant="secondary" onClick={clearFilters}>
                    Reset filter
                  </Button>
                ) : (
                  <Link to="/merchant/invoices/new">
                    <Button>Buat invoice pertama</Button>
                  </Link>
                )
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
                  <Th>Jatuh Tempo</Th>
                  <Th>Aksi</Th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((inv) => (
                  <tr key={inv.id}>
                    <Td>
                      <span className="mono">{inv.invoice_number}</span>
                    </Td>
                    <Td>{inv.customer_name}</Td>
                    <Td numeric>{formatMoney(inv.amount)}</Td>
                    <Td>
                      <StatusBadge status={inv.status} />
                    </Td>
                    <Td>{formatDate(inv.due_date)}</Td>
                    <Td>
                      <Link to={`/merchant/invoices/${inv.id}`}>Detail</Link>
                    </Td>
                  </tr>
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
