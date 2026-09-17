import type { Kind, Mode } from './api'

export const kindName: Record<Kind, string> = { book: 'Книга', paper: 'Статья', vault: 'Заметка', docs: 'Мануал' }

export const kindFilters: { value: Kind | ''; label: string }[] = [
  { value: '', label: 'Всё' },
  { value: 'book', label: 'Книги' },
  { value: 'paper', label: 'Статьи' },
  { value: 'vault', label: 'Заметки' },
  { value: 'docs', label: 'Мануалы' },
]

export const modes: { value: Mode; label: string; hint: string }[] = [
  { value: 'hybrid', label: 'Слова и смысл', hint: 'Оба способа сразу — подходит для большинства вопросов' },
  { value: 'fts', label: 'Точные слова', hint: 'Введённые слова в любой форме, внутри одного языка' },
  { value: 'vector', label: 'По смыслу', hint: 'Фрагменты о том же, даже другими словами и на другом языке' },
]
