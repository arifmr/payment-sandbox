import type { ReactNode } from 'react'
import './DescriptionList.css'

/**
 * Key/value detail rows, used by the invoice detail and payment pages.
 *
 * Uses <dl>/<dt>/<dd> rather than a table: this is a list of properties of one thing, not
 * a grid of records, and the semantics let a screen reader pair each label with its value.
 */
export function DescriptionList({ children }: { children: ReactNode }) {
  return <dl className="dlist">{children}</dl>
}

export function DescriptionItem({
  label,
  children,
  wide,
}: {
  label: ReactNode
  children: ReactNode
  /** Spans both columns, for long text like a description. */
  wide?: boolean
}) {
  return (
    <div className={wide ? 'dlist-item dlist-wide' : 'dlist-item'}>
      <dt className="dlist-label">{label}</dt>
      <dd className="dlist-value">{children}</dd>
    </div>
  )
}
