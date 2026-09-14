import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { sources, type Kind, type SourceStatus } from '../api'
import { UploadPanel } from '../components/UploadPanel'
import { groupManuals, progress, stateLabel } from '../library'
import { plural } from '../text'

const tabs: { kind: Kind; label: string }[] = [
  { kind: 'book', label: 'Книги' },
  { kind: 'docs', label: 'Мануалы' },
  { kind: 'vault', label: 'Заметки' },
]

// The vault alone is thousands of notes; past this many rows a table is
// scrolled, not read, and the filter is the way in.
const shownRows = 300

interface Row {
  key: string
  title: string
  detail: string
  counts: { chunks: number; embedded: number; quarantined: number }
}

function rowsOf(kind: Kind, list: SourceStatus[]): Row[] {
  if (kind === 'docs') {
    return groupManuals(list).map((m) => ({
      key: m.name,
      title: m.name,
      detail: `${m.pages} ${plural(m.pages, ['страница', 'страницы', 'страниц'])}`,
      counts: m,
    }))
  }
  return list.map((s) => ({ key: s.path, title: s.title, detail: s.path, counts: s }))
}

export function LibraryPage({ pollMs = 5000 }: { pollMs?: number }) {
  const [params, setParams] = useSearchParams()
  const kind = (params.get('kind') as Kind | null) ?? 'book'
  const [loaded, setLoaded] = useState<{ kind: Kind; list?: SourceStatus[]; error?: string }>()
  const [generation, setGeneration] = useState(0)
  const [filter, setFilter] = useState('')

  useEffect(() => {
    const request = new AbortController()
    sources({ kind }, request.signal)
      .then((list) => setLoaded({ kind, list }))
      .catch((e: Error) => {
        if (!request.signal.aborted) setLoaded({ kind, error: e.message })
      })
    return () => request.abort()
  }, [kind, generation])

  const refresh = useCallback(() => setGeneration((n) => n + 1), [])

  const current = loaded?.kind === kind ? loaded : undefined
  const rows = useMemo(() => {
    const all = current?.list ? rowsOf(kind, current.list) : []
    const needle = filter.trim().toLocaleLowerCase('ru')
    return needle ? all.filter((r) => r.title.toLocaleLowerCase('ru').includes(needle)) : all
  }, [current, kind, filter])

  return (
    <div className="library">
      <h1>Библиотека</h1>
      <UploadPanel pollMs={pollMs} onSettled={refresh} />

      <div className="library-bar">
        <div className="tabs" role="group" aria-label="Тип источников">
          {tabs.map((t) => (
            <button
              key={t.kind}
              type="button"
              aria-pressed={t.kind === kind}
              onClick={() => setParams(t.kind === 'book' ? {} : { kind: t.kind })}
            >
              {t.label}
            </button>
          ))}
        </div>
        <input type="text" aria-label="Фильтр по названию" placeholder="Фильтр по названию" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </div>

      {!current && <p className="quiet">Загружаю список…</p>}
      {current?.error && (
        <p className="problem" role="alert">
          Список не загрузился: {current.error}
        </p>
      )}
      {current?.list && rows.length === 0 && <p className="quiet">{filter ? 'Ничего не подходит под фильтр.' : 'Здесь пока пусто.'}</p>}
      {rows.length > 0 && (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th scope="col">{kind === 'docs' ? 'Мануал' : 'Название'}</th>
                <th scope="col">{kind === 'docs' ? 'Страниц' : 'Файл'}</th>
                <th scope="col">Состояние</th>
              </tr>
            </thead>
            <tbody>
              {rows.slice(0, shownRows).map((r) => {
                const { state, fraction } = progress(r.counts)
                return (
                  <tr key={r.key} className={`state-${state}`}>
                    <td>{r.title}</td>
                    <td className="detail">{r.detail}</td>
                    <td>
                      <span className="meter" style={{ '--fill': fraction } as React.CSSProperties} aria-hidden="true" />
                      <span>{stateLabel(r.counts)}</span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {rows.length > shownRows && (
        <p className="quiet">
          Показаны первые {shownRows} из {rows.length}. Уточните фильтр, чтобы увидеть остальные.
        </p>
      )}
    </div>
  )
}
