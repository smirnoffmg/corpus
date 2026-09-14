# Working offline

Nothing in corpus needs the network once it is built: Postgres, the indexer,
`mcpd` and the web UI run in compose, embeddings come from ollama on the host,
and the UI bundles its fonts. What does need the network is getting there.

## Before a flight

1. **Build the images while online.** `docker compose build` downloads Go
   modules and npm packages; with no network it fails, and an image that is
   already built does not need either.

   ```sh
   make up
   ```

2. **Have the embedding model pulled.** `ollama pull bge-m3`, and keep ollama
   running (`brew services start ollama`) — without it search falls back to
   exact words, and the UI says so at the top of every page.

3. **Let the embedding queue drain.** Search by words covers a new book or
   manual as soon as it is indexed; search by meaning only once every chunk has
   a vector. The library page shows each source's progress, and a manual the
   size of scikit-learn's takes hours. Leave the laptop plugged in on the
   evening before.

4. **Bring the manuals you will need.** Downloading one is the part that needs
   the network — see [manuals](manuals.md). Uploading and indexing it does not.

## In the air

- `http://localhost:8081/` — search, read, and add books and manuals.
- `http://localhost:8080/mcp` — the same corpus for Claude Code, unchanged.

Search by meaning needs ollama, and ollama needs the GPU; on battery it is
noticeably slower to answer and much slower to embed. Exact-word search costs
nothing.
