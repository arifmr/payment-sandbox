import { useState } from 'react'
import { admin } from '@/api/endpoints'
import type { Refund, RefundAction } from '@/api/types'
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
 * Refund management (SRS §4.4: approve/reject, then process to SUCCESS or FAILED).
 *
 * The available actions are derived from the refund's current status rather than always
 * showing all four. That mirrors the backend state machine — REQUESTED → APPROVED/REJECTED,
 * APPROVED → SUCCESS/FAILED — so the UI cannot offer a transition the server would answer
 * with 422 INVALID_STATE.
 */

/** The transitions legal from each status, mirroring constant.RefundFSM. */
function availableActions(status: Refund['status']): RefundAction[] {
  switch (status) {
    case 'REQUESTED':
      return ['APPROVE', 'REJECT']
    case 'APPROVED':
      return ['PROCESS', 'FAIL']
    // REJECTED, SUCCESS and FAILED are terminal.
    default:
      return []
  }
}

function actionLabel(action: RefundAction): string {
  switch (action) {
    case 'APPROVE':
      return 'Setujui'
    case 'REJECT':
      return 'Tolak'
    case 'PROCESS':
      return 'Cairkan'
    case 'FAIL':
      return 'Tandai Gagal'
  }
}

/** PROCESS debits the merchant's wallet, so it is the one styled as consequential. */
function actionVariant(action: RefundAction): 'success' | 'danger' | 'secondary' {
  switch (action) {
    case 'APPROVE':
      return 'secondary'
    case 'PROCESS':
      return 'success'
    default:
      return 'danger'
  }
}

function confirmText(action: RefundAction, refund: Refund): string {
  switch (action) {
    case 'PROCESS':
      return `Cairkan refund ${formatMoney(refund.amount)}? Saldo merchant akan dipotong dan tindakan ini tidak dapat dibatalkan.`
    case 'APPROVE':
      return 'Setujui refund ini? Saldo belum dipotong sampai refund dicairkan.'
    case 'REJECT':
      return 'Tolak pengajuan refund ini? Status ini bersifat final.'
    case 'FAIL':
      return 'Tandai refund ini gagal? Status ini bersifat final.'
  }
}

export function AdminRefundsPage() {
  const list = usePaginatedList((params, signal) => admin.listRefunds(params, signal), [])

  return (
    <div className="stack">
      <PageHeader
        title="Kelola Refund"
        description="Setujui atau tolak pengajuan, lalu cairkan refund yang sudah disetujui."
      />

      <Card
        title={`Pengajuan Refund (${list.total})`}
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
              description="Pengajuan dari merchant akan muncul di sini."
            />
          }
        >
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Diajukan</Th>
                  <Th>Merchant</Th>
                  <Th>Invoice</Th>
                  <Th numeric>Nominal</Th>
                  <Th>Alasan</Th>
                  <Th>Status</Th>
                  <Th>Aksi</Th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((refund) => (
                  <RefundRow key={refund.id} refund={refund} onActed={list.reload} />
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

function RefundRow({ refund, onActed }: { refund: Refund; onActed: () => void }) {
  const [pendingAction, setPendingAction] = useState<RefundAction | null>(null)

  const action = useAction(async (act: RefundAction) => {
    const updated = await admin.actOnRefund(refund.id, act)
    setPendingAction(null)
    onActed()
    return updated
  })

  const actions = availableActions(refund.status)

  return (
    <>
      <tr>
        <Td>{formatDateTime(refund.created_at)}</Td>
        <Td title={refund.merchant_id}>
          <span className="mono">{shortId(refund.merchant_id)}</span>
        </Td>
        <Td title={refund.invoice_id}>
          <span className="mono">{shortId(refund.invoice_id)}</span>
        </Td>
        <Td numeric>{formatMoney(refund.amount)}</Td>
        <Td>{refund.reason || <span className="muted">—</span>}</Td>
        <Td>
          <StatusBadge status={refund.status} />
        </Td>
        <Td>
          {actions.length === 0 ? (
            <span className="muted text-xs">Sudah final</span>
          ) : (
            <div className="row">
              {actions.map((act) => (
                <Button
                  key={act}
                  size="sm"
                  variant={actionVariant(act)}
                  onClick={() => setPendingAction(act)}
                  disabled={action.isPending}
                >
                  {actionLabel(act)}
                </Button>
              ))}
            </div>
          )}
        </Td>
      </tr>

      {pendingAction && (
        <tr>
          <td colSpan={7}>
            <div className="confirm-bar">
              <p className="text-sm">{confirmText(pendingAction, refund)}</p>
              <div className="row">
                <Button
                  size="sm"
                  variant={actionVariant(pendingAction)}
                  loading={action.isPending}
                  onClick={() => void action.run(pendingAction)}
                >
                  Konfirmasi
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setPendingAction(null)}
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
          <td colSpan={7}>
            <Alert>{errorMessage(action.error)}</Alert>
          </td>
        </tr>
      )}
    </>
  )
}
