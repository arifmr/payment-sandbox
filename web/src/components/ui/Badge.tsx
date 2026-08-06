import { statusLabel } from '@/lib/format'
import './Badge.css'

export type BadgeTone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

/**
 * Maps a domain status to a colour tone.
 *
 * Colour alone must not be the only signal — the badge always shows the text label too,
 * so it stays readable for colour-blind users and in a black-and-white printout.
 */
export function toneForStatus(status: string): BadgeTone {
  switch (status) {
    case 'PAID':
    case 'SUCCESS':
    case 'APPROVED':
      return 'success'
    case 'PENDING':
    case 'REQUESTED':
      return 'warning'
    case 'FAILED':
    case 'REJECTED':
    case 'EXPIRED':
      return 'danger'
    default:
      return 'neutral'
  }
}

interface BadgeProps {
  children: React.ReactNode
  tone?: BadgeTone
}

export function Badge({ children, tone = 'neutral' }: BadgeProps) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

/** Convenience wrapper: picks the tone and the Indonesian label from a status code. */
export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={toneForStatus(status)}>{statusLabel(status)}</Badge>
}
