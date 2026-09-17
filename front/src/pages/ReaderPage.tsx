import { useContext, useEffect, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router'
import { ApiError, read, type Passage } from '../api'
import { Blocks } from '../components/Blocks'
import { CitationPanel } from '../components/CitationPanel'
import { OriginalLink } from '../components/OriginalLink'
import { RecognisedBadge } from '../components/RecognisedBadge'
import { kindName } from '../kinds'
import { originalUrl, paperHref } from '../library'
import { VaultContext } from '../vault'

interface Loaded {
  id: string
  passage?: Passage
  error?: ApiError | Error
}

export function ReaderPage() {
  const { id = '' } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const [loaded, setLoaded] = useState<Loaded>()
  const [copied, setCopied] = useState(false)
  const vault = useContext(VaultContext)

  useEffect(() => {
    const request = new AbortController()
    read(Number(id), request.signal)
      .then((passage) => setLoaded({ id, passage }))
      .catch((error: Error) => {
        if (!request.signal.aborted) setLoaded({ id, error })
      })
    return () => request.abort()
  }, [id])

  // Opened from a link rather than from the results there is no history to go
  // back through, and navigate(-1) would leave the corpus altogether.
  const back =
    location.key === 'default' ? (
      <Link to="/" className="back">
        К поиску
      </Link>
    ) : (
      <button type="button" className="back" onClick={() => navigate(-1)}>
        Назад к результатам
      </button>
    )

  const current = loaded?.id === id ? loaded : undefined
  if (!current) {
    return (
      <div className="reader">
        {back}
        <p className="quiet">Открываю…</p>
      </div>
    )
  }
  if (current.error || !current.passage) {
    const gone = current.error instanceof ApiError && current.error.status === 404
    return (
      <div className="reader">
        {back}
        <p className="problem" role="alert">
          {gone
            ? `Фрагмента №${id} больше нет: источник переиндексирован, и его фрагменты получили новые номера. Повторите поиск.`
            : `Не удалось открыть фрагмент: ${current.error?.message}`}
        </p>
      </div>
    )
  }

  const p = current.passage
  const original = originalUrl(p, vault)
  const copy = async () => {
    await navigator.clipboard.writeText(`${p.title}, ${p.locator}`)
    setCopied(true)
  }

  return (
    <article className={`reader kind-${p.kind}`}>
      {back}
      <header className="reader-head">
        <p className="slip-kind">{kindName[p.kind]}</p>
        <h1>{p.title}</h1>
        <p className="reader-locator">
          {p.locator}
          {p.ocr && <RecognisedBadge />}
        </p>
        <div className="actions">
          <button type="button" onClick={copy}>
            {copied ? 'Скопировано' : 'Скопировать цитату'}
          </button>
          {original && <OriginalLink kind={p.kind} href={original} />}
          {p.kind === 'paper' && <Link to={paperHref(p.path)}>Карточка статьи</Link>}
        </div>
      </header>
      {p.kind !== 'vault' && <CitationPanel passage={p} />}
      {p.previous && (
        <section className="context" aria-labelledby="context-previous">
          <p className="context-label" id="context-previous">
            Предыдущий фрагмент
          </p>
          <Blocks body={p.previous} />
        </section>
      )}
      <div className="passage">
        <Blocks body={p.body} />
      </div>
      {p.next && (
        <section className="context" aria-labelledby="context-next">
          <p className="context-label" id="context-next">
            Следующий фрагмент
          </p>
          <Blocks body={p.next} />
        </section>
      )}
    </article>
  )
}
