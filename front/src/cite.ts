import CSL from 'citeproc'
import type { CSLRecord, Passage, Reference } from './api'
import enUS from './csl/locales-en-US.xml?raw'
import ruRU from './csl/locales-ru-RU.xml?raw'

export interface StyleOption {
  id: string
  title: string
  lang: string
  load: () => Promise<string>
}

// The bundled styles load on demand: APA alone is 85 KB of XML, and most
// pages never cite anything.
export const bundledStyles: StyleOption[] = [
  { id: 'gost', title: 'ГОСТ Р 7.0.100–2018', lang: 'ru-RU', load: () => import('./csl/gost-r-7-0-100-2018.csl?raw').then((m) => m.default) },
  { id: 'apa', title: 'APA 7', lang: 'en-US', load: () => import('./csl/apa.csl?raw').then((m) => m.default) },
  { id: 'ieee', title: 'IEEE', lang: 'en-US', load: () => import('./csl/ieee.csl?raw').then((m) => m.default) },
]

export interface Citable {
  item: CSLRecord & { id: string }
  locator?: string
}

// printedPage reads the page printed in the book out of a locator: a citation
// gives the page a reader of any copy can find, never the PDF's own count.
export function printedPage(locator: string): string | undefined {
  return /^с\. (\d+)/.exec(locator)?.[1]
}

// citationItem is what is cited for a passage: the book itself at the printed
// page, or — for a manual — the page the passage is on, as a web page of the
// manual, at its section.
export function citationItem(ref: Reference, passage: Passage): Citable {
  if (passage.kind !== 'docs') {
    return { item: { ...ref.csl, id: ref.citekey }, locator: printedPage(passage.locator) }
  }
  const manual = ref.csl
  const [name, ...rest] = passage.path.split('/')
  const title = passage.title.startsWith(`${name} · `) ? passage.title.slice(name.length + 3) : passage.title
  const base = typeof manual.URL === 'string' ? manual.URL : ''
  const url = base ? `${base.replace(/\/?$/, '/')}${rest.join('/')}${passage.anchor ? `#${passage.anchor}` : ''}` : undefined
  const item: CSLRecord & { id: string } = {
    id: ref.citekey,
    type: 'webpage',
    title,
    'container-title': manual.title,
  }
  for (const field of ['publisher', 'version', 'accessed', 'language', 'author']) {
    if (manual[field] !== undefined) item[field] = manual[field]
  }
  if (url) item.URL = url
  return { item }
}

export interface Rendered {
  reference: string
  inText: string
}

export function render(style: string, lang: string, { item, locator }: Citable): Rendered {
  const engine = new CSL.Engine(
    {
      retrieveLocale: (l: string) => (l.startsWith('ru') ? ruRU : enUS),
      retrieveItem: () => item,
    },
    style,
    lang,
  )
  engine.setOutputFormat('text')
  engine.updateItems([item.id])
  const bibliography = engine.makeBibliography()
  const reference = bibliography ? bibliography[1].join('').trim().replace(/^\d+\.\s+/, '').replace(/^\[\d+\]\s+/, '') : ''
  const inText = engine.makeCitationCluster([{ id: item.id, ...(locator ? { locator, label: 'page' } : {}) }])
  return { reference, inText }
}

export function describeHref(kind: string, path: string): string {
  return `/describe?${new URLSearchParams({ kind, path })}`
}

export function latex(key: string, locator?: string): string {
  return locator ? `\\autocite[${locator}]{${key}}` : `\\autocite{${key}}`
}

export function pandoc(key: string, locator?: string): string {
  return locator ? `[@${key}, p. ${locator}]` : `[@${key}]`
}
