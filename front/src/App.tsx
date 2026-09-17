import { useEffect, useState } from 'react'
import { Link, NavLink, Route, Routes } from 'react-router'
import { status as fetchStatus, type Status } from './api'
import { DescribePage } from './pages/DescribePage'
import { LibraryPage } from './pages/LibraryPage'
import { PaperPage } from './pages/PaperPage'
import { ReaderPage } from './pages/ReaderPage'
import { SearchPage } from './pages/SearchPage'
import { VaultContext } from './vault'

export function App() {
  const [status, setStatus] = useState<Status | null>(null)

  useEffect(() => {
    const request = new AbortController()
    fetchStatus(request.signal)
      .then(setStatus)
      .catch(() => {})
    return () => request.abort()
  }, [])

  return (
    <VaultContext value={status?.vault}>
    <div className="shell">
      <header className="masthead">
        <Link to="/" className="wordmark">
          Корпус
        </Link>
        <nav aria-label="Разделы">
          <NavLink to="/" end>
            Поиск
          </NavLink>
          <NavLink to="/library">Библиотека</NavLink>
        </nav>
      </header>
      {status?.embedder === 'unreachable' && (
        <p className="banner" role="status">
          Поиск по смыслу недоступен: ollama не отвечает. Поиск по словам работает, новые фрагменты ждут векторизации.
        </p>
      )}
      <main>
        <Routes>
          <Route path="/" element={<SearchPage status={status} />} />
          <Route path="/read/:id" element={<ReaderPage />} />
          <Route path="/library" element={<LibraryPage />} />
          <Route path="/describe" element={<DescribePage />} />
          <Route path="/paper" element={<PaperPage />} />
          <Route
            path="*"
            element={
              <div className="empty">
                <p>Такой страницы нет.</p>
                <Link to="/">Перейти к поиску</Link>
              </div>
            }
          />
        </Routes>
      </main>
    </div>
    </VaultContext>
  )
}
