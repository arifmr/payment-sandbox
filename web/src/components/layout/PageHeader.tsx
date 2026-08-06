import type { ReactNode } from 'react'
import './PageHeader.css'

interface PageHeaderProps {
  title: string
  description?: ReactNode
  actions?: ReactNode
}

/**
 * Page title block. Renders the single <h1> for each page, which keeps the heading
 * hierarchy correct — cards below use <h2>, so a screen reader gets a real outline
 * instead of a flat list of similar-looking headings.
 */
export function PageHeader({ title, description, actions }: PageHeaderProps) {
  return (
    <header className="page-header">
      <div className="page-header-text">
        <h1 className="page-title">{title}</h1>
        {description && <p className="page-desc muted text-sm">{description}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  )
}
