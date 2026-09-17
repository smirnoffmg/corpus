import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError, enrichCitations, getReference, sources, type CSLRecord, type Reference, type SourceStatus } from '../api'
import { describeHref } from '../cite'
import { OriginalLink } from '../components/OriginalLink'
import { RecognisedBadge } from '../components/RecognisedBadge'
import { ReferencesPanel } from '../components/ReferencesPanel'
import { originalUrl, stateLabel } from '../library'

interface Loaded {
  path: string
  source?: SourceStatus
  reference: Reference | null
  error?: string
}

interface Enrichment {
  busy: boolean
  message?: string
  problem?: string
}

const descriptionState: Record<string, string> = { checked: 'Проверено', draft: 'Черновик' }

function authorsOf(csl: CSLRecord): string {
  const list = Array.isArray(csl.author) ? (csl.author as Record<string, unknown>[]) : []
  return list
    .map((a) => (typeof a.literal === 'string' ? a.literal : [a.family, a.given].filter(Boolean).join(', ')))
    .filter(Boolean)
    .join('; ')
}

function yearOf(csl: CSLRecord): string {
  const parts = (csl.issued as { 'date-parts'?: unknown[][] } | undefined)?.['date-parts']
  const year = parts?.[0]?.[0]
  return year === undefined ? '' : String(year)
}

function enrichProblem(e: unknown): string {
  if (e instanceof ApiError && (e.status === 502 || e.status === 503)) {
    return 'Запрос к doi.org не удался: дополнение по DOI работает только с интернетом.'
  }
  return `Дополнить не удалось: ${e instanceof Error ? e.message : String(e)}`
}

// PaperPage is a publication's card: what it is, as its description says; where
// its PDF is; what it cites and what in the corpus cites it. A reference list is
// read out of the PDF and can be thin, so the card is also where it is filled in
// from doi.org — on request only, since that is the network.
export function PaperPage() {
  const [params] = useSearchParams()
  const path = params.get('path') ?? ''
  const [loaded, setLoaded] = useState<Loaded>()
  // Bumped after enrichment, so the reference list is read again.
  const [generation, setGeneration] = useState(0)
  const [enrichment, setEnrichment] = useState<Enrichment>({ busy: false })

  useEffect(() => {
    const request = new AbortController()
    Promise.all([
      sources({ kind: 'paper', prefix: path }, request.signal),
      getReference('paper', path, request.signal).catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return { key: '', reference: null }
        throw e
      }),
    ])
      .then(([list, { reference }]) => setLoaded({ path, source: list.find((s) => s.path === path), reference }))
      .catch((e: Error) => {
        if (!request.signal.aborted) setLoaded({ path, reference: null, error: e.message })
      })
    return () => request.abort()
  }, [path])

  const back = (
    <Link to="/library?kind=paper" className="back">
      К статьям
    </Link>
  )
  const current = loaded?.path === path ? loaded : undefined
  if (!current) {
    return (
      <div className="paper">
        {back}
        <p className="quiet">Открываю статью…</p>
      </div>
    )
  }
  if (current.error || !current.source) {
    return (
      <div className="paper">
        {back}
        <p className="problem" role="alert">
          {current.error ? `Статья не открылась: ${current.error}` : `Такой статьи в библиотеке нет: ${path}`}
        </p>
      </div>
    )
  }

  const { source, reference } = current
  const csl = reference?.csl ?? {}
  const authors = authorsOf(csl)
  const venue = [csl['container-title'], yearOf(csl)].filter(Boolean).join(', ')
  const doi = typeof csl.DOI === 'string' ? csl.DOI : ''
  const pdf = originalUrl({ kind: 'paper', path })

  const enrich = async () => {
    setEnrichment({ busy: true })
    try {
      const { asked, filled } = await enrichCitations(path)
      setEnrichment({
        busy: false,
        message: asked === 0
          ? 'Запрашивать нечего: у всех записей с DOI уже есть заглавие, авторы и год.'
          : `Дополнено записей: ${filled} из ${asked} запрошенных.`,
      })
      setGeneration((n) => n + 1)
    } catch (e) {
      setEnrichment({ busy: false, problem: enrichProblem(e) })
    }
  }

  return (
    <article className="paper">
      {back}
      <header className="reader-head">
        <p className="slip-kind">Статья</p>
        <h1>{source.title}</h1>
        {authors && <p className="paper-authors">{authors}</p>}
        {venue && <p className="paper-venue">{venue}</p>}
        {doi && (
          <p className="paper-doi">
            DOI{' '}
            <a href={`https://doi.org/${doi}`} target="_blank" rel="noreferrer noopener">
              {doi}
            </a>
          </p>
        )}
        <p className="quiet">
          {path} · {source.stage === 'indexed' ? stateLabel(source) : 'Распознаётся'}
          {source.stage === 'indexed' && source.ocr && <RecognisedBadge />}
        </p>
        <div className="actions">
          {pdf && (
            <OriginalLink kind="paper" href={pdf}>
              Открыть PDF
            </OriginalLink>
          )}
          <Link to={describeHref('paper', path)}>Изменить описание</Link>
          <span className={`description-${reference?.status ?? 'none'}`}>
            {reference ? descriptionState[reference.status] : 'Не описана'}
          </span>
        </div>
      </header>

      <div className="paper-enrich">
        <button type="button" onClick={enrich} disabled={enrichment.busy}>
          {enrichment.busy ? 'Запрашиваю doi.org…' : 'Дополнить по DOI'}
        </button>
        <span className="quiet">Заглавие, авторы и год для записей, где разобран только DOI. Нужен интернет.</span>
        {enrichment.message && <p role="status">{enrichment.message}</p>}
        {enrichment.problem && (
          <p className="problem" role="alert">
            {enrichment.problem}
          </p>
        )}
      </div>

      <ReferencesPanel key={generation} path={path} />
    </article>
  )
}
