import type { ReactNode } from 'react'
import './Table.css'

/**
 * Table primitives.
 *
 * The wrapper is what matters: it scrolls horizontally on its own so a wide table never
 * makes the whole page scroll sideways on a phone (SRS §4.1, responsive). Semantic
 * <table> markup is kept — a div grid would lose row/column relationships for screen
 * readers.
 */
export function TableWrap({ children }: { children: ReactNode }) {
  return (
    <div className="table-wrap" tabIndex={0} role="group">
      {children}
    </div>
  )
}

export function Table({ children }: { children: ReactNode }) {
  return <table className="table">{children}</table>
}

/** `numeric` right-aligns and tabularises, so money columns line up on the decimal. */
export function Th({ children, numeric }: { children: ReactNode; numeric?: boolean }) {
  return <th className={numeric ? 'is-numeric' : undefined}>{children}</th>
}

export function Td({
  children,
  numeric,
  title,
}: {
  children: ReactNode
  numeric?: boolean
  title?: string
}) {
  return (
    <td className={numeric ? 'is-numeric tabular' : undefined} {...(title ? { title } : {})}>
      {children}
    </td>
  )
}
