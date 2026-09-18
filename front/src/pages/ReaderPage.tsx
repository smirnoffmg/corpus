import { useContext, useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router'
import { ApiError, read, type Passage } from '../api'
import { Blocks } from '../components/Blocks'
import { CitationPanel } from '../components/CitationPanel'
import { OriginalLink } from '../components/OriginalLink'
import { RecognisedBadge } from '../components/RecognisedBadge'
import { kindName } from '../kinds'
import { originalUrl, paperHref } from '../library'
import { VaultContext } from '../vault'

// selectedIn is the text selected inside el, whitespace folded, or '' when the
// selection is empty or reaches outside it.
function selectedIn(el: HTMLElement | null): string {
  const selection = window.getSelection()
  if (!el || !selection || selection.isCollapsed || !el.contains(selection.anchorNode) || !el.contains(selection.focusNode)) return ''
  return selection.toString().replace(/\s+/g, ' ').trim()
}

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
  const body = useRef<HTMLDivElement>(null)

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
    const place = `${p.title}, ${p.locator}`
    const quote = selectedIn(body.current)
    await navigator.clipboard.writeText(quote ? `«${quote}»\n— ${place}` : place)
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
          {/* Pressing a button collapses the selection it is to quote. */}
          <button type="button" onMouseDown={(e) => e.preventDefault()} onClick={copy} title="Выделенный текст фрагмента войдёт в цитату">
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
      <div className="passage" ref={body}>
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
