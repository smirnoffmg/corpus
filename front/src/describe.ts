import type { CSLRecord } from './api'

export type Field = { name: string; label: string; kind?: 'names' | 'year' | 'date' }

export const types: { value: string; label: string }[] = [
  { value: 'book', label: 'Книга' },
  { value: 'article-journal', label: 'Статья в журнале' },
  { value: 'chapter', label: 'Глава в книге' },
  { value: 'paper-conference', label: 'Доклад конференции' },
  { value: 'thesis', label: 'Диссертация' },
  { value: 'report', label: 'Отчёт' },
  { value: 'webpage', label: 'Сайт или документация' },
]

const f = (name: string, label: string, kind?: Field['kind']): Field => ({ name, label, kind })

const common = [f('author', 'Авторы', 'names'), f('title', 'Заглавие')]

// Which fields a type asks for follows what GOST and APA print for it: a
// journal article has a volume and pages, a book a publisher and a page count.
export const fieldsByType: Record<string, Field[]> = {
  book: [...common, f('editor', 'Редакторы', 'names'), f('translator', 'Переводчики', 'names'), f('edition', 'Издание'), f('publisher-place', 'Место издания'), f('publisher', 'Издательство'), f('issued', 'Год', 'year'), f('number-of-pages', 'Число страниц'), f('collection-title', 'Серия'), f('ISBN', 'ISBN'), f('DOI', 'DOI')],
  'article-journal': [...common, f('container-title', 'Журнал'), f('issued', 'Год', 'year'), f('volume', 'Том'), f('issue', 'Номер'), f('page', 'Страницы'), f('DOI', 'DOI'), f('URL', 'URL')],
  chapter: [...common, f('container-title', 'Книга'), f('editor', 'Редакторы', 'names'), f('publisher-place', 'Место издания'), f('publisher', 'Издательство'), f('issued', 'Год', 'year'), f('page', 'Страницы'), f('ISBN', 'ISBN'), f('DOI', 'DOI')],
  'paper-conference': [...common, f('container-title', 'Сборник трудов'), f('editor', 'Редакторы', 'names'), f('publisher-place', 'Место издания'), f('publisher', 'Издательство'), f('issued', 'Год', 'year'), f('page', 'Страницы'), f('DOI', 'DOI')],
  thesis: [...common, f('genre', 'Вид работы'), f('publisher', 'Организация'), f('publisher-place', 'Город'), f('issued', 'Год', 'year'), f('number-of-pages', 'Число страниц')],
  report: [...common, f('publisher', 'Организация'), f('publisher-place', 'Город'), f('issued', 'Год', 'year'), f('number-of-pages', 'Число страниц'), f('URL', 'URL')],
  webpage: [...common, f('container-title', 'Сайт'), f('publisher', 'Кто публикует'), f('version', 'Версия'), f('URL', 'URL'), f('accessed', 'Дата обращения', 'date')],
}

export const languages = [
  { value: '', label: 'не указан' },
  { value: 'ru', label: 'русский' },
  { value: 'en', label: 'английский' },
]

type Name = { family?: string; given?: string; literal?: string }

// Names are edited one per line as "Фамилия, Имя"; a line without a comma is
// an organisation or a name that must not be inverted.
export function namesToText(value: unknown): string {
  if (!Array.isArray(value)) return ''
  return (value as Name[])
    .map((n) => (n.literal ? n.literal : [n.family, n.given].filter(Boolean).join(', ')))
    .join('\n')
}

export function textToNames(text: string): Name[] | undefined {
  const names = text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((line): Name => {
      const [family, ...given] = line.split(',')
      return given.length ? { family: family.trim(), given: given.join(',').trim() } : { literal: line }
    })
  return names.length ? names : undefined
}

function dateParts(value: unknown): number[] | undefined {
  const parts = (value as { 'date-parts'?: unknown[][] } | undefined)?.['date-parts']?.[0]
  return parts?.map(Number)
}

export function fieldText(csl: CSLRecord, field: Field): string {
  const value = csl[field.name]
  switch (field.kind) {
    case 'names':
      return namesToText(value)
    case 'year':
      return dateParts(value)?.[0]?.toString() ?? ''
    case 'date': {
      const [y, m, d] = dateParts(value) ?? []
      return y ? [y, m, d].filter((x) => x !== undefined).map((x, i) => (i ? String(x).padStart(2, '0') : String(x))).join('-') : ''
    }
    default:
      return value === undefined || value === null ? '' : String(value)
  }
}

// withField writes one edited field back into the record, keeping every
// field the form does not show — a looked-up record carries more than the
// form has room for, and none of it should be lost by an edit.
export function withField(csl: CSLRecord, field: Field, text: string): CSLRecord {
  const next = { ...csl }
  const trimmed = text.trim()
  // Stored as typed: trimming on every keystroke would swallow the space
  // between two words before the second is typed.
  let value: unknown = trimmed ? text : undefined
  if (trimmed) {
    if (field.kind === 'names') value = textToNames(text)
    if (field.kind === 'year') value = { 'date-parts': [[Number(trimmed)]] }
    if (field.kind === 'date') value = { 'date-parts': [trimmed.split('-').map(Number)] }
  }
  if (value === undefined) delete next[field.name]
  else next[field.name] = value
  return next
}

// changes lists the fields a looked-up record would change, for review before
// anything is replaced.
export function changes(current: CSLRecord, found: CSLRecord): { field: Field; now: string; found: string }[] {
  const fields = fieldsByType[String(found.type ?? current.type)] ?? fieldsByType.book
  return fields
    .map((field) => ({ field, now: fieldText(current, field), found: fieldText(found, field) }))
    .filter((c) => c.found && c.found !== c.now)
}
