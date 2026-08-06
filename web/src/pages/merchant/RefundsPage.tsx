import { refunds } from '@/api/endpoints'
import { usePaginatedList } from '@/hooks/usePaginatedList'
import { formatDateTime, formatMoney, shortId } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { Pagination } from '@/components/ui/Pagination'
import { AsyncBoundary, EmptyState } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/**
 * The merchant's own refunds (SRS §4.2).
 *
 * Read-only: a refund is *requested* from an invoice's detail page, because it needs the
 * invoice context to bound the amount. Listing it separately here gives the merchant one
 * place to track progress through the approval flow.
 */
export function RefundsPage() {
  const list = usePaginatedList((params, signal) => refunds.listMine(params, signal), [])

  return (
    <div className="stack">
      <PageHeader
        title="Refund"
        description="Status pengajuan refund Anda. Pengajuan baru dilakukan dari halaman detail invoice."
      />

      <Card
        title={`Daftar Refund (${list.total})`}
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
          loadingLabel="Memuat refund"
          empty={
            <EmptyState
              title="Belum ada pengajuan refund"
              description="Buka detail invoice yang sudah dibayar untuk mengajukan refund."
            />
          }
        >
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Diajukan</Th>
                  <Th>Invoice</Th>
                  <Th numeric>Nominal</Th>
                  <Th>Alasan</Th>
                  <Th>Status</Th>
                  <Th>Diproses</Th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((r) => (
                  <tr key={r.id}>
                    <Td>{formatDateTime(r.created_at)}</Td>
                    {/* Full id in the title so it can be copied when needed, without
                        making the column unreadably wide. */}
                    <Td title={r.invoice_id}>
                      <span className="mono">{shortId(r.invoice_id)}</span>
                    </Td>
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
