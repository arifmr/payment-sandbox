import type { ReactNode } from 'react'
import './Card.css'

interface CardProps {
  title?: ReactNode
  description?: ReactNode
  /** Rendered on the header's trailing edge — filters, actions. */
  actions?: ReactNode
  children: ReactNode
  /** Removes body padding, for a table that should meet the card edge. */
  flush?: boolean
}

export function Card({ title, description, actions, children, flush = false }: CardProps) {
  return (
    <section className="card">
      {(title || actions) && (
        <header className="card-head">
          <div className="card-head-text">
            {title && <h2 className="card-title">{title}</h2>}
            {description && <p className="card-desc">{description}</p>}
          </div>
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      <div className={flush ? 'card-body-flush' : 'card-body'}>{children}</div>
    </section>
  )
}

interface MetricProps {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: 'default' | 'success' | 'danger' | 'warning'
}

/** A single dashboard statistic (SRS §2.6 / §4.4). */
export function Metric({ label, value, hint, tone = 'default' }: MetricProps) {
  return (
    <div className={`metric metric-${tone}`}>
      <span className="metric-label">{label}</span>
      <strong className="metric-value tabular">{value}</strong>
      {hint && <span className="metric-hint">{hint}</span>}
    </div>
  )
}
