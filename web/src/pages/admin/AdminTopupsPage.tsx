import { useState } from 'react'
import { admin } from '@/api/endpoints'
import type { SettleAction, Topup } from '@/api/types'
import { useAction } from '@/hooks/useAsync'
import { usePaginatedList } from '@/hooks/usePaginatedList'
import { formatDateTime, formatMoney, shortId } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { Pagination } from '@/components/ui/Pagination'
import { Alert, AsyncBoundary, EmptyState, errorMessage } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/**
 * Top-up settlement (SRS §2.2: admin sets SUCCESS or FAILED; SUCCESS credits the wallet).
 *
 * SUCCESS creates balance out of nothing in this sandbox, so it is the most consequential
 * action in the admin area and is gated behind a confirmation.
 */
export function AdminTopupsPage() {
  const list = usePaginatedList((params, signal) => admin.listTopups(params, signal), [])

  return (
    <div className="stack">
      <PageHeader
        title="Kelola Top-up"
        description="Setujui atau tolak permintaan top-up merchant. Menyetujui akan menambah saldo."
      />

      <Card
        title={`Permintaan Top-up (${list.total})`}
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
          loadingLabel="Memuat top-up"
          empty={
            <EmptyState
              title="Belum ada permintaan top-up"
              description="Permintaan dari merchant akan muncul di sini."
            />
          }
        >
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Diajukan</Th>
                  <Th>Merchant</Th>
                  <Th numeric>Nominal</Th>
                  <Th>Status</Th>
                  <Th>Diproses</Th>
                  <Th>Aksi</Th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((topup) => (
                  <TopupRow key={topup.id} topup={topup} onSettled={list.reload} />
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

function TopupRow({ topup, onSettled }: { topup: Topup; onSettled: () => void }) {
  const [confirming, setConfirming] = useState<SettleAction | null>(null)

  const action = useAction(async (settle: SettleAction) => {
    const updated = await admin.settleTopup(topup.id, settle)
    setConfirming(null)
    onSettled()
    return updated
  })

  const settled = topup.status !== 'PENDING'

  return (
    <>
      <tr>
        <Td>{formatDateTime(topup.created_at)}</Td>
        <Td title={topup.merchant_id}>
          <span className="mono">{shortId(topup.merchant_id)}</span>
        </Td>
        <Td numeric>{formatMoney(topup.amount)}</Td>
        <Td>
          <StatusBadge status={topup.status} />
        </Td>
        <Td>{formatDateTime(topup.processed_at)}</Td>
        <Td>
          {settled ? (
            <span className="muted text-xs">Sudah final</span>
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

      {confirming && (
        <tr>
          <td colSpan={6}>
            <div className="confirm-bar">
              <p className="text-sm">
                {confirming === 'SUCCESS'
                  ? `Setujui top-up ${formatMoney(topup.amount)}? Saldo merchant akan langsung bertambah.`
                  : 'Tolak permintaan top-up ini? Status ini bersifat final.'}
              </p>
              <div className="row">
                <Button
                  size="sm"
                  variant={confirming === 'SUCCESS' ? 'success' : 'danger'}
                  loading={action.isPending}
                  onClick={() => void action.run(confirming)}
                >
                  Konfirmasi
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
            </div>
          </td>
        </tr>
      )}

      {action.error && (
        <tr>
          <td colSpan={6}>
            <Alert>{errorMessage(action.error)}</Alert>
          </td>
        </tr>
      )}
    </>
  )
}
