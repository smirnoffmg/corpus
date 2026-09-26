import { useEffect, useState, type DragEvent } from 'react'
import { ApiError, sources, upload } from '../api'
import { combine, manualName, nothingYet, progress, stateLabel, type Tally } from '../library'
import { plural } from '../text'

interface Recent {
  path: string
  label: string
  prefix: string
  tally?: Tally
  settled: boolean
}

const accepted = /\.(pdf|epub|fb2|zip)$/i

function refusal(e: unknown): string {
  if (!(e instanceof ApiError)) return `Загрузка не удалась: ${String(e)}`
  switch (e.status) {
    case 0:
      return 'Нет связи с сервером. Проверьте, что mcpd запущен.'
    case 409:
      return `Такой файл уже есть в библиотеке: ${e.message}`
    case 413:
      return `Файл слишком большой для загрузки: ${e.message}`
    case 400:
      return `Файл не принят: ${e.message}`
    case 503:
      return `Загрузка выключена на сервере: ${e.message}`
    default:
      return `Загрузка не удалась: ${e.message}`
  }
}

type QueueState = 'waiting' | 'sending' | 'refused' | 'unsuitable'

interface Queued {
  id: string
  file: File
  kind: 'book' | 'paper'
  manual: string
  state: QueueState
  sent: number
  reason?: string
}

const unsuitable = 'не подойдёт: загружаются только .pdf (книга или статья), .epub и .fb2 (книга) и .zip со сборкой HTML-мануала'

const isZip = (f: File) => /\.zip$/i.test(f.name)

// Publications are PDFs only: their reference lists are read page by page.
const isEbook = (f: File) => /\.(epub|fb2)$/i.test(f.name)

// A file chosen twice — picked again, or dropped after being picked — is the
// same upload. Name and size, not the modification time: the server refuses a
// second file of the same name anyway, so a copy with a fresher timestamp is
// no different upload.
const identity = (f: File) => `${f.name}\u0000${f.size}`

// UploadPanel sends books, publications and manuals and then follows them
// through indexing.
// Files are queued and sent one at a time: a shelf of books at once would
// multiply the load on the disk and on the indexer for nothing, and a file the
// server refuses stops only itself. A file that is uploaded but not yet indexed
// has no row anywhere, so the panel keeps its own list of what it sent.
export function UploadPanel({ pollMs, onSettled }: { pollMs: number; onSettled: () => void }) {
  const [queue, setQueue] = useState<Queued[]>([])
  const [sending, setSending] = useState(false)
  const [over, setOver] = useState(false)
  const [recent, setRecent] = useState<Recent[]>([])

  const choose = (files: FileList | File[] | null | undefined) => {
    if (!files) return
    const chosen = Array.from(files)
    setQueue((q) => {
      const known = new Set(q.map((item) => identity(item.file)))
      const added = chosen
        .filter((f) => !known.has(identity(f)) && known.add(identity(f)))
        .map((f): Queued => ({
          id: identity(f),
          file: f,
          kind: 'book',
          manual: manualName(f.name),
          state: accepted.test(f.name) ? 'waiting' : 'unsuitable',
          sent: 0,
          reason: accepted.test(f.name) ? undefined : unsuitable,
        }))
      return [...q, ...added]
    })
  }

  const drop = (e: DragEvent) => {
    e.preventDefault()
    setOver(false)
    choose(e.dataTransfer.files)
  }

  const update = (id: string, change: Partial<Queued>) =>
    setQueue((q) => q.map((item) => (item.id === id ? { ...item, ...change } : item)))

  const ready = queue.filter((item) => item.state === 'waiting')
  const unnamed = ready.some((item) => isZip(item.file) && !item.manual.trim())

  const send = async () => {
    setSending(true)
    for (const item of ready) {
      update(item.id, { state: 'sending', sent: 0, reason: undefined })
      try {
        const target = isZip(item.file) ? { kind: 'docs' as const, manual: item.manual.trim() } : { kind: item.kind }
        const done = await upload(item.file, target, (sent) => update(item.id, { sent }))
        const prefix = done.kind === 'docs' ? `${done.path}/` : done.path
        setRecent((r) => [{ path: done.path, label: item.file.name, prefix, settled: false }, ...r])
        setQueue((q) => q.filter((other) => other.id !== item.id))
      } catch (e) {
        update(item.id, { state: 'refused', reason: refusal(e) })
      }
    }
    setSending(false)
  }

  useEffect(() => {
    if (!recent.some((r) => !r.settled)) return
    const timer = setTimeout(async () => {
      const next = await Promise.all(
        recent.map(async (r) => {
          if (r.settled) return r
          try {
            const rows = await sources({ prefix: r.prefix })
            const tally = combine(rows)
            const { state } = progress(tally)
            return { ...r, tally, settled: state === 'ready' || state === 'errors' }
          } catch {
            return r
          }
        }),
      )
      setRecent(next)
      if (next.some((r, i) => r.settled && !recent[i].settled)) onSettled()
    }, pollMs)
    return () => clearTimeout(timer)
  }, [recent, pollMs, onSettled])

  return (
    <section className="upload" aria-labelledby="upload-title">
      <h2 id="upload-title">Добавить в библиотеку</h2>
      <div
        className={over ? 'dropzone over' : 'dropzone'}
        onDragOver={(e) => {
          e.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={drop}
      >
        <p>Перетащите сюда PDF книг и статей, EPUB и FB2 книг или ZIP со сборками мануалов — можно несколько сразу.</p>
        <label className="file-button">
          Выбрать файлы
          <input
            type="file"
            accept=".pdf,.epub,.fb2,.zip"
            multiple
            className="visually-hidden"
            onChange={(e) => {
              choose(e.target.files)
              // Cleared, so choosing the same file again after removing it still
              // fires a change.
              e.target.value = ''
            }}
          />
        </label>
      </div>

      {queue.length > 0 && (
        <section aria-label="Очередь загрузки" className="queue">
          <ul>
            {queue.map((item) => (
              <li key={item.id} className={`queued queued-${item.state}`}>
                <div className="queued-head">
                  <span className="queued-name">{item.file.name}</span>
                  {isZip(item.file) ? (
                    <span className="queued-kind">Мануал</span>
                  ) : isEbook(item.file) ? (
                    <span className="queued-kind">Книга</span>
                  ) : accepted.test(item.file.name) ? (
                    <>
                      <label htmlFor={`kind-${item.id}`} className="visually-hidden">
                        Что это за файл: «{item.file.name}»
                      </label>
                      <select
                        id={`kind-${item.id}`}
                        className="queued-kind"
                        value={item.kind}
                        disabled={item.state === 'sending'}
                        onChange={(e) => update(item.id, { kind: e.target.value as 'book' | 'paper' })}
                      >
                        <option value="book">Книга</option>
                        <option value="paper">Статья</option>
                      </select>
                    </>
                  ) : (
                    <span className="queued-kind" />
                  )}
                  {item.state !== 'sending' && (
                    <button type="button" className="queued-remove" onClick={() => setQueue((q) => q.filter((other) => other.id !== item.id))} aria-label={`Убрать «${item.file.name}»`} disabled={sending && item.state === 'waiting'}>
                      Убрать
                    </button>
                  )}
                </div>
                {isZip(item.file) && item.state !== 'unsuitable' && (
                  <div className="field">
                    <label htmlFor={`manual-${item.id}`} className="visually-hidden">
                      Название мануала для «{item.file.name}»
                    </label>
                    <input
                      id={`manual-${item.id}`}
                      type="text"
                      value={item.manual}
                      placeholder="Название мануала"
                      disabled={item.state === 'sending'}
                      onChange={(e) => update(item.id, { manual: e.target.value, state: item.state === 'refused' ? 'waiting' : item.state, reason: undefined })}
                    />
                    <small>В цитатах: «{item.manual.trim() || '…'} · страница».</small>
                  </div>
                )}
                {item.state === 'sending' && (
                  <span className="sending">
                    <progress value={item.sent} max={1} /> Загружаю… {Math.round(item.sent * 100)}%
                  </span>
                )}
                {item.state === 'unsuitable' && <p className="problem">{item.reason}</p>}
                {item.state === 'refused' && (
                  <p className="problem" role="alert">
                    {item.reason}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <div className="upload-actions">
        <button type="button" onClick={send} disabled={sending || ready.length === 0 || unnamed}>
          Загрузить
        </button>
        {ready.length > 0 && !sending && (
          <span className="quiet">
            {ready.length} {plural(ready.length, ['файл', 'файла', 'файлов'])}
          </span>
        )}
        {unnamed && <span className="quiet">У мануала должно быть название.</span>}
      </div>

      {recent.length > 0 && (
        <section aria-label="Загрузки" className="recent">
          <ul>
            {recent.map((r) => (
              <li key={r.path} className={`state-${progress(r.tally ?? nothingYet).state}`}>
                <span>{r.label}</span>
                <span>{stateLabel(r.tally ?? nothingYet)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </section>
  )
}
