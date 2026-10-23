# Is it used

The [search evaluation](search-evaluation.md) says whether the right page comes
first. It does not say whether a search reaches the reader: whether a book
copied in is searchable yet, whether the agents' queries find anything, whether
a quote ends up in a note. Three things answer that, cheapest first.

## Files not indexed yet

`GET /status` lists under `unindexed` every book and publication on disk that
has no source in the index, and the system page shows them. A file copied into
the library rather than uploaded sends no reindex request, so it waits for the
next pass; a file still listed after a pass is one the pass could not read.

The pass interval is counted by the wall clock. It used to be a timer, and a
timer runs on the monotonic clock, which stands still in Docker's VM while the
Mac sleeps: twelve minutes asleep delayed a pass by twelve minutes, with four
new books already on disk and nothing to say so.
