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

// titleLanguage guesses a source's language from its title, for descriptions
// that do not state one — which is every draft. A majority rather than a share,
// because a Russian shelf tag in an English title is common in this library.
export function titleLanguage(title: string): 'ru' | 'en' {
  const cyrillic = title.match(/\p{Script=Cyrillic}/gu)?.length ?? 0
  const latin = title.match(/\p{Script=Latin}/gu)?.length ?? 0
  return cyrillic > latin ? 'ru' : 'en'
}

// citationItem is what is cited for a passage: the book itself at the printed
// page, or — for a manual — the page the passage is on, as a web page of the
// manual, at its section.
export function citationItem(ref: Reference, passage: Passage): Citable {
  if (passage.kind !== 'docs') {
    const language = ref.csl.language ?? titleLanguage(String(ref.csl.title ?? ''))
    return { item: { ...ref.csl, language, id: ref.citekey }, locator: printedPage(passage.locator) }
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
  // numbered: the in-text reference carries a number that only a list of
  // references can give, shown as N.
  numbered: boolean
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
  const cited = engine.makeCitationCluster([{ id: item.id, ...(locator ? { locator, label: 'page' } : {}) }])
  // Cited alone, every source is number 1; the number precedes the page, so
  // the first standalone 1 is the source's even when the page is 1 too.
  const numbered = /citation-format="numeric"/.test(style)
  const inText = numbered ? cited.replace(/(?<!\d)1(?!\d)/, 'N') : cited
  return { reference, inText, numbered }
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
