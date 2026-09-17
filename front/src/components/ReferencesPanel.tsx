import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { citations, citing, type Citation, type CitingPaper, type Kind } from '../api'
import { paperHref } from '../library'
import { plural } from '../text'

interface Loaded {
  cited: Citation[]
  citing: CitingPaper[]
}

// libraryLink opens a cited work where the corpus shows it best: a publication
// on its card, anything else on its shelf, filtered down to the file.
function libraryLink(kind: Kind | undefined, path: string): string {
  if (kind === 'paper') return paperHref(path)
  return `/library?${new URLSearchParams({ kind: kind ?? 'book', filter: path })}`
}

// where a citation points: into the library when the work is already there,
// out to doi.org when it is not, and nowhere at all when the entry named
// neither — the line as printed is then all there is, which is why it is shown
// whole.
function Target({ c }: { c: Citation }) {
  if (c.resolved_path) {
    return <Link to={libraryLink(c.resolved_kind, c.resolved_path)}>{c.resolved_title || c.resolved_path}</Link>
  }
  if (c.doi) {
    return (
      <a href={`https://doi.org/${c.doi}`} target="_blank" rel="noreferrer noopener">
        {c.doi}
      </a>
    )
  }
  if (c.arxiv) {
    return (
      <a href={`https://arxiv.org/abs/${c.arxiv}`} target="_blank" rel="noreferrer noopener">
        arXiv:{c.arxiv}
      </a>
    )
  }
  return null
}

// Parsed is what was read out of an entry, under the entry itself: a reader
// sees at a glance whether the parse is right, and what doi.org filled in.
function Parsed({ c }: { c: Citation }) {
  const rest = [c.authors, c.container, c.year].filter(Boolean).join(' · ')
  if (!c.title && !rest) return null
  return (
    <span className="reference-parsed">
      {c.title && <cite>{c.title}</cite>}
      {c.title && rest && ' · '}
      {rest}
    </span>
  )
}

function Entries({ entries }: { entries: Citation[] }) {
  return (
    <ol className="reference-list">
      {entries.map((c) => (
        <li key={`${c.list}-${c.ord}`}>
          <span className="reference-raw">{c.raw}</span>
          <Parsed c={c} />
          <span className="reference-target">
            <Target c={c} />
          </span>
        </li>
      ))}
    </ol>
  )
}

// ReferencesPanel shows what a publication cites and who cites it — and, for a
// systematic review, the studies it reviewed, as a list of their own. The entry
// as printed is the text: everything parsed out of it can be wrong, and a
// reader checking a citation wants the line they would find on the page.
export function ReferencesPanel({ path }: { path: string }) {
  const [loaded, setLoaded] = useState<Loaded>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const request = new AbortController()
    Promise.all([citations(path, request.signal), citing(path, request.signal)])
      .then(([cited, by]) => setLoaded({ cited, citing: by }))
      .catch(() => {
        if (!request.signal.aborted) setFailed(true)
      })
    return () => request.abort()
  }, [path])

  const citingPapers = new Set(loaded?.citing.map((p) => p.path)).size

  if (failed) return null
  if (!loaded) return <p className="quiet">Читаю список литературы…</p>

  const references = loaded.cited.filter((c) => c.list !== 'primary')
  const studies = loaded.cited.filter((c) => c.list === 'primary')

  return (
    <>
      {studies.length > 0 && (
        <section className="references" aria-labelledby="studies-title">
          <h2 id="studies-title">Первичные исследования: {studies.length}</h2>
          <Entries entries={studies} />
        </section>
      )}
      <section className="references" aria-labelledby="references-title">
        <h2 id="references-title">Ссылается на</h2>
        {references.length === 0 ? (
          <p className="quiet">Список литературы не распознан — или статья его не печатает.</p>
        ) : (
          <Entries entries={references} />
        )}

        {loaded.citing.length > 0 && (
          <section aria-labelledby="citing-title">
            <h3 id="citing-title">
              На эту работу ссылаются: {citingPapers} {plural(citingPapers, ['публикация', 'публикации', 'публикаций'])}
            </h3>
            <ul className="citing-list">
              {loaded.citing.map((p) => (
                <li key={`${p.path}-${p.citation.list}-${p.citation.ord}`}>
                  <Link to={libraryLink('paper', p.path)}>{p.title}</Link>
                  {p.citation.list === 'primary' && <span className="citing-as">включила как первичное исследование</span>}
                  <span className="quiet">{p.citation.raw}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </section>
    </>
  )
}
