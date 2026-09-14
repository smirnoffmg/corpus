declare module 'citeproc' {
  interface Sys {
    retrieveLocale(lang: string): string
    retrieveItem(id: string): unknown
  }
  interface Engine {
    setOutputFormat(format: 'text' | 'html'): void
    updateItems(ids: string[]): void
    makeBibliography(): [unknown, string[]] | false
    makeCitationCluster(items: { id: string; locator?: string; label?: string }[]): string
  }
  const CSL: { Engine: new (sys: Sys, style: string, lang?: string, forceLang?: boolean) => Engine }
  export default CSL
}
