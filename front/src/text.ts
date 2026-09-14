export interface Segment {
  text: string
  marked: boolean
}

// ts_headline wraps matches in << and >>. A marker only counts where it can
// mean something — << outside a match, >> inside one — because manual snippets
// are full of Python prompts, and ">>> import" is not the end of a match.
export function highlightSegments(snippet: string): Segment[] {
  const out: Segment[] = []
  let marked = false
  let run = ''
  const push = () => {
    if (run) out.push({ text: run, marked })
    run = ''
  }
  for (let i = 0; i < snippet.length; i++) {
    const pair = snippet.slice(i, i + 2)
    if ((!marked && pair === '<<') || (marked && pair === '>>' && snippet[i + 2] !== '>')) {
      push()
      marked = !marked
      i++
      continue
    }
    run += snippet[i]
  }
  push()
  return out
}

export interface Block {
  kind: 'text' | 'code'
  content: string
}

// blocks reads a chunk body back into paragraphs and code: the extractor keeps
// code as fenced blocks, and blank lines inside code are code.
export function blocks(body: string): Block[] {
  const out: Block[] = []
  let code: string[] | null = null
  let para: string[] = []
  const endParagraph = () => {
    const text = para.join('\n').trim()
    if (text) out.push({ kind: 'text', content: text })
    para = []
  }
  for (const line of body.split('\n')) {
    if (line.trim().startsWith('```')) {
      if (code) {
        out.push({ kind: 'code', content: code.join('\n') })
        code = null
      } else {
        endParagraph()
        code = []
      }
      continue
    }
    if (code) code.push(line)
    else if (line.trim() === '') endParagraph()
    else para.push(line)
  }
  if (code) out.push({ kind: 'code', content: code.join('\n') })
  endParagraph()
  return out
}

// plural picks the Russian form for a count: 1 фрагмент, 2 фрагмента, 5 фрагментов.
export function plural(n: number, [one, few, many]: [string, string, string]): string {
  const mod10 = n % 10
  const mod100 = n % 100
  if (mod10 === 1 && mod100 !== 11) return one
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few
  return many
}
