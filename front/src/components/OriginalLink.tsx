import type { ReactNode } from 'react'
import type { Kind } from '../api'
import { originalLabel } from '../library'

// A PDF or a manual page opens beside the corpus in a new tab; a note opens in
// the Obsidian app, and a new tab for an obsidian:// link would stay blank.
export function OriginalLink({
  kind,
  href,
  className,
  children,
}: {
  kind: Kind
  href: string
  className?: string
  children?: ReactNode
}) {
  const tab = kind === 'vault' ? {} : { target: '_blank', rel: 'noreferrer' }
  return (
    <a href={href} className={className} {...tab}>
      {children ?? originalLabel(kind)}
    </a>
  )
}
