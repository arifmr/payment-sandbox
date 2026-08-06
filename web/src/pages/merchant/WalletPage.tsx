import { useState } from 'react'
import { wallet } from '@/api/endpoints'
import { useAction, useAsync } from '@/hooks/useAsync'
import { usePaginatedList } from '@/hooks/usePaginatedList'
import { formatDateTime, formatMoney } from '@/lib/format'
import { validateAmount } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { Card, Metric } from '@/components/ui/Card'
import { StatusBadge } from '@/components/ui/Badge'
import { TextField } from '@/components/ui/Field'
import { Table, TableWrap, Td, Th } from '@/components/ui/Table'
import { Pagination } from '@/components/ui/Pagination'
import { Alert, AsyncBoundary, EmptyState, errorMessage } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/** Wallet balance and top-up simulation (SRS §2.2 / §4.2). */
export function WalletPage() {
  const balance = useAsync((signal) => wallet.get(signal), [])
  const topups = usePaginatedList((params, signal) => wallet.listMine(params, signal), [])

  // Both panels read the same underlying data, so a successful top-up request has to
  // refresh the list, and settling one (by an admin) changes the balance.
  function refreshAll() {
    balance.reload()
    topups.reload()
  }

  return (
    <div className="stack">
      <PageHeader
        title="Wallet & Top-up"
        description="Saldo simulasi. Top-up baru bertambah setelah admin menyetujuinya."
      />

      <div className="grid-metrics">
        <Metric
          label="Saldo Saat Ini"
          value={
            balance.isLoading ? '…' : balance.data ? formatMoney(balance.data.balance) : '—'
          }
          hint={
            balance.data ? `Diperbarui ${formatDateTime(balance.data.updated_at)}` : undefined
          }
        />
      </div>

      {balance.error && (
        <Alert>
          {errorMessage(balance.error)}{' '}
          <button className="link-button" onClick={balance.reload}>
            Coba lagi
          </button>
        </Alert>
      )}

      <TopupForm onCreated={refreshAll} />

      <Card
        title={`Riwayat Top-up (${topups.total})`}
        actions={
          <Button variant="ghost" size="sm" onClick={refreshAll} disabled={topups.isLoading}>
            Muat ulang
          </Button>
        }
        flush
      >
        <AsyncBoundary
          isLoading={topups.isLoading}
          error={topups.error}
          isEmpty={topups.isEmpty}
          onRetry={topups.reload}
          loadingLabel="Memuat riwayat top-up"
          empty={
            <EmptyState
              title="Belum ada top-up"
              description="Ajukan top-up di atas untuk menambah saldo simulasi."
            />
          }
        >
          <TableWrap>
            <Table>
              <thead>
                <tr>
                  <Th>Waktu</Th>
                  <Th numeric>Nominal</Th>
                  <Th>Status</Th>
                  <Th>Diproses</Th>
                </tr>
              </thead>
              <tbody>
                {topups.items.map((t) => (
                  <tr key={t.id}>
                    <Td>{formatDateTime(t.created_at)}</Td>
                    <Td numeric>{formatMoney(t.amount)}</Td>
                    <Td>
                      <StatusBadge status={t.status} />
                    </Td>
                    <Td>{formatDateTime(t.processed_at)}</Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </TableWrap>

          <Pagination
            page={topups.page}
            pageSize={topups.pageSize}
            total={topups.total}
            totalPages={topups.totalPages}
            onPageChange={topups.setPage}
            disabled={topups.isLoading}
          />
        </AsyncBoundary>
      </Card>
    </div>
  )
}

function TopupForm({ onCreated }: { onCreated: () => void }) {
  const [amount, setAmount] = useState('')
  const [error, setError] = useState<string | undefined>()
  const [done, setDone] = useState(false)

  const action = useAction(async (value: number) => {
    const topup = await wallet.requestTopup(value)
    setAmount('')
    setDone(true)
    onCreated()
    return topup
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setDone(false)
    const err = validateAmount(amount)
    setError(err)
    if (err) return
    void action.run(Number(amount))
  }

  return (
    <Card
      title="Ajukan Top-up"
      description="Top-up dibuat dengan status PENDING dan tidak langsung menambah saldo."
    >
      <form className="stack" onSubmit={handleSubmit} noValidate>
        {action.error && <Alert>{errorMessage(action.error)}</Alert>}
        {done && !action.error && (
          <Alert tone="success">
            Top-up berhasil diajukan. Saldo bertambah setelah admin menyetujuinya.
          </Alert>
        )}

        <TextField
          label="Nominal Top-up (IDR)"
          type="number"
          inputMode="numeric"
          min={1}
          step={1}
          value={amount}
          error={error}
          required
          placeholder="100000"
          onChange={(e) => {
            setAmount(e.target.value)
            if (error) setError(undefined)
            if (done) setDone(false)
            if (action.error) action.reset()
          }}
        />

        <div className="row">
          <Button type="submit" loading={action.isPending}>
            Ajukan Top-up
          </Button>
        </div>
      </form>
    </Card>
  )
}
