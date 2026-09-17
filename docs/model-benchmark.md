# Model benchmark

How the models for fully local operation are to be chosen: the embedding model
that search runs on, and the language model that does development and design
work on top of the corpus. This is a method written before the hardware
arrives, so that the first run measures what was decided in advance rather
than whatever looks good on the day. Parts of it do not exist yet; the last
section lists them.

## Why local, and what that rules out

The corpus holds a personal diary and private notes, and the intended use —
applied development and design grounded in what the corpus says — puts its
passages into the model's context on every step. So every model runs on a
machine on the local network, and no cloud model is a candidate, however good.
Models that Ollama offers only as `:cloud` tags are out for the same reason.

The target machine is a DGX Spark class box: 128 GB of unified memory with a
bandwidth of about 273 GB/s, and a Blackwell GPU. That shape decides what is
worth measuring. Memory is plentiful, so large models fit; bandwidth is modest,
so generation speed follows the parameters *active* per token rather than the
model's size, and mixture-of-experts models are the natural candidates.
Prompt processing is where this class of machine is strong, and in agentic work
it is where the time goes — every page the agent reads is thousands of tokens
to process before a single token comes back.

## Rules for every run

- **Measure here, on this corpus and these tasks.** Public leaderboards pick
  candidates; they do not pick the winner. A model that leads a multilingual
  benchmark still has to find a Russian question's answer in an English book
  of this library.
- **One variable at a time.** The same chunks, the same queries or tasks, the
  same machine; only the model changes. That is how bge-m3 was chosen over
  granite-embedding (below), and why that comparison took eight minutes.
- **Decide the thresholds before the first run.** A bar set after seeing the
  numbers is set to fit them.
- **Compare query by query, or task by task,** with a sign test, as
  [search evaluation](search-evaluation.md) does. On sets this small a moved
  mean is often a handful of items.
- **Numbers compare only within one corpus state,** with the embedding queue
  drained — see [search evaluation](search-evaluation.md) for why a run taken
  while vectors are still being written measures the queue.
- **Rent before buying.** Every step below runs the same on a rented machine
  of the target class, and a few hours of it cost less than a wrong purchase.

## Part 1. The embedding model

### Where things stand

Search embeds with **bge-m3** (1024 dimensions), served by Ollama. It was
compared with granite-embedding:278m on the same vault chunks and kept: it
found 87% of the judged answers against 73%, with MRR 0.683 against 0.572,
while granite embedded about twice as fast. Two other models were dropped
earlier by a cheaper test (below), since they stopped reading long before the
window the corpus hands them ([architecture](architecture.md#embeddings)).

That choice was made on a laptop, where a full re-embed was the expensive
part. On the target machine it is not, which opens larger models.

### Candidates

Multilingual embedding models in the Ollama library as of September 2026:

| model | sizes | context | notes |
| --- | --- | --- | --- |
| `bge-m3` (baseline) | 567m | — | current model |
| `qwen3-embedding` | 0.6b, 4b, 8b (639 MB, 2.5 GB, 4.7 GB) | 32K–40K | output dimension configurable from 32 to 4096; the 8b ranked first on the multilingual MTEB leaderboard in June 2025 |
| `snowflake-arctic-embed2` | 568m | — | multilingual |
| `nomic-embed-text-v2-moe` | — | — | multilingual mixture of experts |
| `embeddinggemma` | 300m | — | — |

### Procedure

1. **A separate stack per candidate.** Its own database, an indexer and an
   `mcpd` started with `--model` set to the candidate, on a port of their own,
   over the same library and vault. The vector columns are created at the
   candidate's dimension in that database — the schema fixes `vector(1024)`,
   and the granite comparison ran at 768 the same way.
2. **Drain the queue,** then run the judged sets against each stack:
   `go run ./cmd/eval -addr <stack> -v` for the private set, and
   `-queries eval/example.json` for the public one (52 queries, 24
   paraphrases, 14 in Russian).
3. **Compare with the baseline query by query** in `vector` and `hybrid`
   mode.
4. **Check how much of the window the model reads.** Embed a page, then its
   first 400, 1200 and 2400 characters, and see where the vector stops
   changing. The corpus hands the embedder up to 5000 characters of a chunk
   with 500 of each neighbour; a model that stops at 1200 is not reading most
   of it, whatever its declared context.
5. **Time it:** a full re-embed of the corpus (about 85 thousand chunks), and a
   single query's embedding against `mcpd`'s budget of one attempt in three
   seconds.

### What decides

- **The paraphrases and the cross-language queries.** They are what the vector
  leg is for; exact terms are the text index's job. A candidate has to win
  there, by a sign test, without losing exact queries in `hybrid`.
- **Recall stays where it is.** `-recall` at `ef_search=200` should not fall;
  a different vector space can make the index harder to walk.
- **The window test passes** at the corpus's window size.
- **Cost is a tiebreaker only:** re-embed time and query latency within the
  budget.

### Switching

A vector is filed under the SHA-256 of the text it was computed from, and the
model is not part of that key. A model changed in place would keep the old
vectors of every chunk whose text did not change, and the index would mix two
vector spaces. So a switch is one migration that empties `embeddings` and sets
the new dimension, followed by a full re-embed — cheap on the target machine,
and simpler than keeping vectors per model.

## Part 2. The language model

### The task

An agent working on a real repository: it searches the corpus, reads the
passages that carry a design argument, cites them by locator, changes code and
runs the tests, in a loop. What matters is that the loop holds together —
one malformed tool call or one invented citation ends a task — and how long it
takes, since each step adds code, test output and corpus passages to a context
that easily passes 64K tokens.

### Candidates

From the Ollama library as of September 2026, all with tool calling:

| model | parameters, total / active | size | notes |
| --- | --- | --- | --- |
| `qwen3.5:122b` | 125B / 10B (Qwen3.5-122B-A10B) | 81 GB, Q4_K_M | hybrid linear and full attention, cheap on long context; vision |
| `gpt-oss:120b` | 117B, MoE | 65 GB, MXFP4 | configurable reasoning effort; the one with published figures on this hardware (below) |
| `qwen3.8:27b` | 27B, dense | 18 GB | aimed at long-horizon agentic work; dense, so slower per token |
| `qwen3.6` | 27b, 35b | 18 GB, 23 GB | aimed at agentic coding |
| `qwen3.5:35b` | 36B / 3B (Qwen3.5-35B-A3B) | 24 GB | fast; leaves room for long context and the embedder |
| `gemma4:26b` | 25.2B / 3.8B | 19 GB | sliding-window attention over most layers |

A reference point for speed: llama.cpp's published run of `gpt-oss:120b` on a
DGX Spark processed prompts at 1854 tokens/s on an empty context and 849 at
32K, and generated at 35 and 23 tokens/s respectively
([llama.cpp discussion #16578](https://github.com/ggml-org/llama.cpp/discussions/16578)).

### The task set

Five to ten tasks, each fixed as:

- a repository and the commit it starts from;
- the request, in the words a person would use;
- the command that decides success — the tests the task must make pass;
- the sources the task should draw on, as corpus paths, where a design
  argument is part of the answer.

Past commits of this repository are a ready source: check out the parent,
state the problem the commit solved, and take the tests the commit added as
the acceptance check. The tests are then written by someone who did not know
which model would face them. Tasks that touch private notes or work code live
outside the repository, like the private query set
(`~/.corpus/eval/tasks/`).

### What is measured, per task and model

- **Success:** the acceptance command passes.
- **Grounding:** the agent searched the corpus and read what it cited.
- **Citations:** each cited locator resolves with `corpus_read`, and the quoted
  text is on that page. This is checked mechanically, not by eye: an invented
  quote is the failure that matters most in this setting.
- **Tool-call errors:** malformed calls, calls to tools that do not exist.
- **Steps and wall time,** and prompt-processing and generation speed at the
  context the task reached.
- **Peak memory,** so that a model and the embedder are known to fit together.

Each task runs at least three times per model — agents are not deterministic,
and one lucky run is not a result — at the sampling settings the model's card
recommends, with a context of 64K.

### What decides

Set before the first run, in this order: the success rate; then citations,
where a model that invents one is out regardless of success; then median wall
time per task against a limit chosen in advance. Among models within reach of
the best, the faster one wins — a task that takes a quarter of an hour is a
task that does not get delegated.

## What exists, and what is to be built

| piece | state |
| --- | --- |
| judged query sets and `cmd/eval` | exist |
| query-by-query comparison against a baseline | exists within one server (`-against`); across two servers, one per embedding model, it is to be built |
| the embedder's address in `compose.yaml` | to be built: `indexer` and `mcpd` take Ollama's address from a flag default, not from `.env` |
| an output dimension sent to the embedder | to be built, and Ollama's support for it checked; `internal/embed` does not send one |
| a script that raises a candidate stack on its own database | to be built |
| the agent client | to be chosen: it needs files and a terminal, this MCP server, and a model served by Ollama over the network |
| the task set and the citation checker | to be built |
