import { useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { sources, type Description, type Kind, type SourceStatus } from '../api'
import { describeHref } from '../cite'
import { OriginalLink } from '../components/OriginalLink'
import { StylesPanel } from '../components/StylesPanel'
import { UploadPanel } from '../components/UploadPanel'
import { RecognisedBadge } from '../components/RecognisedBadge'
import { groupManuals, originalUrl, paperHref, progress, stateLabel, type Tally } from '../library'
import { VaultContext } from '../vault'
import { plural } from '../text'

const tabs: { kind: Kind; label: string }[] = [
  { kind: 'book', label: 'Книги' },
  { kind: 'paper', label: 'Статьи' },
  { kind: 'docs', label: 'Мануалы' },
  { kind: 'vault', label: 'Заметки' },
]

const descriptionLabel: Record<Description, string> = { '': 'Описать', draft: 'Черновик', checked: 'Проверено' }

// The vault alone is thousands of notes; past this many rows a table is
// scrolled, not read, and the filter is the way in.
const shownRows = 300

interface Row {
  key: string
  title: string
  href: string | null
  card: string | null // a publication's page in the corpus, which its title opens
  describe: { path: string; status: Description } | null
  detail: string
  tally: Tally
  ocr: boolean
  problem?: string
}

function rowsOf(kind: Kind, list: SourceStatus[], vault?: string): Row[] {
  if (kind === 'docs') {
    const pages = list.filter((s) => s.stage === 'indexed')
    return groupManuals(pages).map((m) => ({
      key: m.name,
      title: m.name,
      href: originalUrl({ kind, path: m.home }),
      card: null,
      describe: { path: m.home, status: m.description },
      detail: `${m.pages} ${plural(m.pages, ['страница', 'страницы', 'страниц'])}`,
      tally: m,
      ocr: false,
    }))
  }
  return list.map((s) => {
    const row = { key: s.path, title: s.title, href: originalUrl(s, vault), card: null, detail: s.path, tally: s }
    // A scan not yet indexed has no source to hang a description on, and no
    // card: there is nothing read out of it yet.
    if (s.stage === 'scan') return { ...row, describe: null, ocr: false, problem: s.error }
    const described = kind === 'book' || kind === 'paper'
    return {
      ...row,
      card: kind === 'paper' ? paperHref(s.path) : null,
      describe: described ? { path: s.path, status: s.description } : null,
      ocr: s.ocr,
    }
  })
}

export function LibraryPage({ pollMs = 5000 }: { pollMs?: number }) {
  const [params, setParams] = useSearchParams()
  const kind = (params.get('kind') as Kind | null) ?? 'book'
  const [loaded, setLoaded] = useState<{ kind: Kind; list?: SourceStatus[]; error?: string }>()
  const [generation, setGeneration] = useState(0)
  // Seeded from the query string, so a link elsewhere can point at one source:
  // a citation that was matched to a book links to the book itself.
  const [filter, setFilter] = useState(params.get('filter') ?? '')
  const vault = useContext(VaultContext)

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
    const all = current?.list ? rowsOf(kind, current.list, vault) : []
    const needle = filter.trim().toLocaleLowerCase('ru')
    return needle ? all.filter((r) => r.title.toLocaleLowerCase('ru').includes(needle)) : all
  }, [current, kind, filter, vault])

  return (
    <div className="library">
      <h1>Библиотека</h1>
      <UploadPanel pollMs={pollMs} onSettled={refresh} />

      <div className="library-exports">
        <span className="quiet">Все описания для LaTeX и Pandoc:</span>
        <a href="/api/bibliography/export?format=biblatex" download>
          corpus.bib
        </a>
        <a href="/api/bibliography/export?format=csl-json" download>
          corpus.json
        </a>
      </div>

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
                {kind !== 'vault' && <th scope="col">Описание</th>}
              </tr>
            </thead>
            <tbody>
              {rows.slice(0, shownRows).map((r) => {
                const { state, fraction } = progress(r.tally)
                return (
                  <tr key={r.key} className={`state-${state}`}>
                    <td>
                      {r.card ? (
                        <>
                          <Link to={r.card}>{r.title}</Link>
                          {r.href && (
                            <>
                              {' '}
                              <OriginalLink kind={kind} href={r.href} className="quiet" label={`PDF: ${r.title}`}>
                                PDF
                              </OriginalLink>
                            </>
                          )}
                        </>
                      ) : r.href ? (
                        <OriginalLink kind={kind} href={r.href}>
                          {r.title}
                        </OriginalLink>
                      ) : (
                        r.title
                      )}
                    </td>
                    <td className="detail">{r.detail}</td>
                    <td>
                      <span className="meter" style={{ '--fill': fraction } as React.CSSProperties} aria-hidden="true" />
                      <span title={state === 'errors' ? r.problem : undefined}>{stateLabel(r.tally)}</span>
                      {r.ocr && <RecognisedBadge />}
                    </td>
                    {(kind === 'book' || kind === 'paper') && !r.describe && <td />}
                    {r.describe && (
                      <td className={`description-${r.describe.status || 'none'}`}>
                        <Link to={describeHref(kind, r.describe.path)} aria-label={`Описание: ${r.title}`}>
                          {descriptionLabel[r.describe.status]}
                        </Link>
                      </td>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      <StylesPanel />
      {rows.length > shownRows && (
        <p className="quiet">
          Показаны первые {shownRows} из {rows.length}. Уточните фильтр, чтобы увидеть остальные.
        </p>
      )}
    </div>
  )
}
