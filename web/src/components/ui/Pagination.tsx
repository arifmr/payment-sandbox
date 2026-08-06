import { Button } from './Button'
import './Pagination.css'

interface PaginationProps {
  page: number
  pageSize: number
  total: number
  totalPages: number
  onPageChange: (page: number) => void
  disabled?: boolean
}

/**
 * Offset pagination controls, matching the backend's page/page_size/total envelope.
 *
 * Deliberately prev/next plus a position readout rather than numbered pages: the backend
 * uses OFFSET, whose cost grows with depth, so the UI does not invite a jump to page 500.
 */
export function Pagination({
  page,
  pageSize,
  total,
  totalPages,
  onPageChange,
  disabled = false,
}: PaginationProps) {
  if (total === 0) return null

  const first = (page - 1) * pageSize + 1
  const last = Math.min(page * pageSize, total)

  return (
    <nav className="pagination" aria-label="Navigasi halaman">
      <p className="pagination-info text-sm muted">
        Menampilkan <strong>{first}</strong>–<strong>{last}</strong> dari{' '}
        <strong>{total}</strong> data
      </p>
      <div className="pagination-controls">
        <Button
          variant="secondary"
          size="sm"
          onClick={() => onPageChange(page - 1)}
          disabled={disabled || page <= 1}
        >
          Sebelumnya
        </Button>
        <span className="pagination-page text-sm" aria-current="page">
          {page} / {totalPages}
        </span>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => onPageChange(page + 1)}
          disabled={disabled || page >= totalPages}
        >
          Berikutnya
        </Button>
      </div>
    </nav>
  )
}
