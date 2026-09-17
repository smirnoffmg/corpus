import type { ReactNode } from 'react'
import type { Kind } from '../api'
import { originalLabel } from '../library'

// A PDF or a manual page opens beside the corpus in a new tab; a note opens in
// the Obsidian app, and a new tab for an obsidian:// link would stay blank.
export function OriginalLink({
  kind,
  href,
  className,
  label,
  children,
}: {
  kind: Kind
  href: string
  className?: string
  label?: string
  children?: ReactNode
}) {
  const tab = kind === 'vault' ? {} : { target: '_blank', rel: 'noreferrer' }
  return (
    <a href={href} className={className} aria-label={label} {...tab}>
      {children ?? originalLabel(kind)}
    </a>
  )
}
