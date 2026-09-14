import { vi } from 'vitest'

type Route = (url: URL) => { status?: number; body: unknown } | undefined

// stubApi answers fetch by URL, the way the corpus API would, and records what
// was asked; an unrouted request fails the test loudly instead of hanging.
export function stubApi(route: Route) {
  const calls: URL[] = []
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://corpus.test')
    calls.push(url)
    const answer = route(url)
    if (!answer) throw new Error(`unexpected request ${url.pathname}${url.search}`)
    const text = typeof answer.body === 'string' ? answer.body : JSON.stringify(answer.body)
    return new Response(text, { status: answer.status ?? 200 })
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}
