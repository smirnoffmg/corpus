// Command eval measures retrieval quality against a hand-built set of judged
// queries. Ranking cannot be tuned honestly without it: every knob — length
// normalisation, hnsw.ef, the fusion constant — trades one query against
// another, and only a fixed set makes that trade visible.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
	"github.com/smirnoffmg/corpus/internal/evalstat"
)

// A judgement names a source by a substring of its path, or a vault note by its
// whole path with Note — a substring "RAG.md" would also match "GraphRAG.md".
// Section narrows a note to the chunks under that heading, at any depth.
type judgement struct {
	Path    string `json:"path"`
	Pages   []int  `json:"pages"`
	Note    string `json:"note"`
	Section string `json:"section"`
}

type query struct {
	ID       string      `json:"id"`
	Style    string      `json:"style"`
	Kind     string      `json:"kind"`
	Query    string      `json:"query"`
	Relevant []judgement `json:"relevant"`
}

type score struct {
	queries      int
	reciprocal   float64 // summed 1/rank of the first relevant hit
	precisionAt5 float64
	found        int // queries with a relevant hit in the top 10
}

func (s score) mrr() float64 { return s.reciprocal / float64(s.queries) }
func (s score) p5() float64  { return s.precisionAt5 / float64(s.queries) }
func (s score) hit() float64 { return float64(s.found) / float64(s.queries) }

func main() {
	addr := flag.String("addr", "http://localhost:8080", "corpus server")
	// The judged set is private: its answers are notes of the vault and pages of
	// books in the library, so it lives beside the library, not in the
	// repository. eval/example.json is a public set on freely available manuals.
	path := flag.String("queries", defaultQueries(), "judged query set")
	limit := flag.Int("limit", 10, "hits to request per query")
	verbose := flag.Bool("v", false, "list the queries each mode misses")
	norm := flag.Int("norm", 0, "ts_rank_cd length normalisation bit mask to measure")
	boost := flag.Float64("title-boost", 0, "rank added when the source title matches the query; unset leaves the service default")
	only := flag.String("kind", "", "measure only queries of this kind: book, vault or docs")
	set := flag.String("set", "", "extra search parameters for the measured run, e.g. depth=100,ef_search=200")
	against := flag.String("against", "", "also run with these parameters (\"\" for the defaults: -against=default) and compare query by query")
	recall := flag.String("recall", "", "measure vector recall against an exact scan at these ef_search values, e.g. 40,100,200")
	k := flag.Int("k", 20, "neighbours compared when measuring recall")
	save := flag.String("save", "", "write each query's reciprocal rank per mode to this file")
	baseline := flag.String("baseline", "", "compare query by query with a file written by -save")
	flag.Parse()

	// Sending title_boost unconditionally would measure a search the service
	// never performs: unset means "whatever the service does", which is the
	// thing worth measuring.
	boostSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "title-boost" {
			boostSet = true
		}
	})

	extra, err := parseParams(*set)
	if err != nil {
		log.Fatal(err)
	}

	raw, err := os.ReadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	var queries []query
	if jsonErr := json.Unmarshal(raw, &queries); jsonErr != nil {
		log.Fatal(jsonErr)
	}

	if *only != "" {
		kept := queries[:0]
		for _, q := range queries {
			if q.Kind == *only {
				kept = append(kept, q)
			}
		}
		queries = kept
	}

	if *recall != "" {
		measureRecall(*addr, queries, *recall, *k, extra)
		return
	}

	modes := []string{"fts", "vector", "hybrid"}
	// A hand-written set says exact or paraphrase; a generated one says where
	// each query came from. Report whatever groups the set has, in its order.
	var styles []string
	for _, q := range queries {
		if !slices.Contains(styles, q.Style) {
			styles = append(styles, q.Style)
		}
	}

	overall := map[string]*score{}
	byStyle := map[string]map[string]*score{}
	misses := map[string][]string{}
	for _, m := range modes {
		overall[m] = &score{}
		byStyle[m] = map[string]*score{}
		for _, st := range styles {
			byStyle[m][st] = &score{}
		}
	}

	rr := map[string]map[string]float64{} // mode → query id → reciprocal rank
	for _, m := range modes {
		rr[m] = map[string]float64{}
	}
	for _, q := range queries {
		for _, mode := range modes {
			hits, searchErr := search(*addr, &q, mode, *limit, *norm, *boost, boostSet, extra)
			if searchErr != nil {
				log.Fatalf("%s/%s: %v", q.ID, mode, searchErr)
			}
			s := measure(&q, hits)
			rr[mode][q.ID] = s.reciprocal
			add(overall[mode], s)
			add(byStyle[mode][q.Style], s)
			if s.found == 0 {
				misses[mode] = append(misses[mode], q.ID)
			}
		}
	}

	measured := overall["fts"].queries
	fmt.Printf("%d queries measured\n\n", measured)
	fmt.Printf("%-8s %-18s %7s %7s %8s\n", "mode", "queries", "MRR", "P@5", "found@10")
	for _, mode := range modes {
		report(mode, "all", overall[mode])
		for _, st := range styles {
			report("", st, byStyle[mode][st])
		}
	}

	if *against != "" {
		baselineParams := url.Values{}
		if *against != "default" {
			if baselineParams, err = parseParams(*against); err != nil {
				log.Fatal(err)
			}
		}
		base := map[string]map[string]float64{}
		for _, mode := range modes {
			base[mode] = map[string]float64{}
			for _, q := range queries {
				hits, err := search(*addr, &q, mode, *limit, *norm, *boost, boostSet, baselineParams)
				if err != nil {
					log.Fatalf("%s/%s: %v", q.ID, mode, err)
				}
				base[mode][q.ID] = measure(&q, hits).reciprocal
			}
		}
		compare("against "+describe(baselineParams), modes, styles, queries, base, rr)
	}

	// A change to the index — what a chunk carries, how it is cut — cannot be
	// switched per request the way a ranking parameter can, so the run before
	// it is saved and the run after it compared with the file.
	if *save != "" {
		data, err := json.MarshalIndent(rr, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*save, append(data, '\n'), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	if *baseline != "" {
		data, err := os.ReadFile(*baseline)
		if err != nil {
			log.Fatal(err)
		}
		var base map[string]map[string]float64
		if err := json.Unmarshal(data, &base); err != nil {
			log.Fatal(err)
		}
		compare("against "+*baseline, modes, styles, queries, base, rr)
	}

	if *verbose {
		fmt.Println()
		for _, mode := range modes {
			if len(misses[mode]) > 0 {
				fmt.Printf("%s finds nothing for: %s\n", mode, strings.Join(misses[mode], ", "))
			}
		}
	}
}

// compare prints a candidate run against a baseline, query by query, for each
// mode and each group of the set.
func compare(title string, modes, styles []string, queries []query, base, cand map[string]map[string]float64) {
	fmt.Printf("\n%s, query by query (reciprocal rank):\n", title)
	fmt.Printf("%-8s %-18s %5s %6s %5s %9s %7s %7s\n", "mode", "queries", "wins", "losses", "ties", "ΔMRR", "p sign", "p rand")
	row := func(mode, shown, label string, ids map[string]bool) {
		b, c := map[string]float64{}, map[string]float64{}
		for id, v := range base[mode] {
			if ids == nil || ids[id] {
				b[id] = v
			}
		}
		for id, v := range cand[mode] {
			if ids == nil || ids[id] {
				c[id] = v
			}
		}
		r := evalstat.Compare(b, c)
		if r.Wins+r.Losses+r.Ties == 0 {
			return
		}
		fmt.Printf("%-8s %-18s %5d %6d %5d %+9.3f %7.3f %7.3f\n", shown, label, r.Wins, r.Losses, r.Ties, r.MeanDelta, r.P, r.PRandom)
	}
	for _, mode := range modes {
		row(mode, mode, "all", nil)
		if len(styles) < 2 {
			continue
		}
		for _, st := range styles {
			ids := map[string]bool{}
			for _, q := range queries {
				if q.Style == st {
					ids[q.ID] = true
				}
			}
			row(mode, "", st, ids)
		}
	}
	fmt.Println("p sign counts wins and losses; p rand, a randomization test, also weighs how far each query moved.")
	fmt.Println("Both are two-sided; on a few dozen queries treat p > 0.05 as no difference.")
}

// parseParams reads "depth=100,ef_search=200" into query parameters.
func parseParams(s string) (url.Values, error) {
	out := url.Values{}
	for _, pair := range strings.Split(s, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("parameter %q is not key=value", pair)
		}
		out.Set(key, value)
	}
	return out, nil
}

func describe(v url.Values) string {
	if len(v) == 0 {
		return "the defaults"
	}
	return v.Encode()
}

// measureRecall compares, per query, the k nearest chunks the index returns at
// each ef_search against an exact scan. Recall below one means the index is
// losing true neighbours before ranking even starts — the cost of approximate
// search, measured rather than assumed (Malkov, Yashunin, с. 5: ef trades
// speed for recall).
func measureRecall(addr string, queries []query, efs string, k int, extra url.Values) {
	values := strings.Split(efs, ",")
	fmt.Printf("vector recall@%d against an exact scan, %d queries\n\n", k, len(queries))
	fmt.Printf("%10s %8s %8s %12s\n", "ef_search", "mean", "min", "full recall")
	for _, ef := range values {
		ef = strings.TrimSpace(ef)
		sum, low, full := 0.0, 1.0, 0
		for _, q := range queries {
			exactParams := url.Values{"exact": {"1"}}
			approxParams := url.Values{"ef_search": {ef}}
			for key, vals := range extra {
				exactParams[key], approxParams[key] = vals, vals
			}
			exact, err := search(addr, &q, "vector", k, 0, 0, false, exactParams)
			if err != nil {
				log.Fatalf("%s exact: %v", q.ID, err)
			}
			approx, err := search(addr, &q, "vector", k, 0, 0, false, approxParams)
			if err != nil {
				log.Fatalf("%s ef %s: %v", q.ID, ef, err)
			}
			r := evalstat.Recall(ids(exact), ids(approx))
			sum += r
			low = min(low, r)
			if r == 1 {
				full++
			}
		}
		fmt.Printf("%10s %8.3f %8.3f %8d/%d\n", ef, sum/float64(len(queries)), low, full, len(queries))
	}
}

func ids(hits []corpus.Hit) []int64 {
	out := make([]int64, len(hits))
	for i := range hits {
		out[i] = hits[i].ID
	}
	return out
}

func defaultQueries() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "eval/example.json"
	}
	return filepath.Join(home, ".corpus", "eval", "queries.json")
}

func report(mode, label string, s *score) {
	if s.queries == 0 {
		return
	}
	fmt.Printf("%-8s %-18s %7.3f %7.3f %7.0f%%\n", mode, label, s.mrr(), s.p5(), s.hit()*100)
}

func add(into *score, from score) {
	into.queries += from.queries
	into.reciprocal += from.reciprocal
	into.precisionAt5 += from.precisionAt5
	into.found += from.found
}

// measure scores one result list: the rank of the first relevant hit, how much
// of the top five is relevant, and whether anything relevant showed up at all.
func measure(q *query, hits []corpus.Hit) score {
	s := score{queries: 1}
	relevantInTop5 := 0
	for i := range hits {
		if !isRelevant(q, &hits[i]) {
			continue
		}
		if s.reciprocal == 0 {
			s.reciprocal = 1 / float64(i+1)
			s.found = 1
		}
		if i < 5 {
			relevantInTop5++
		}
	}
	s.precisionAt5 = float64(relevantInTop5) / 5
	return s
}

func isRelevant(q *query, h *corpus.Hit) bool {
	for _, r := range q.Relevant {
		if r.Note != "" {
			if h.Path == r.Note && (r.Section == "" || slices.Contains(strings.Split(h.Locator, " > "), r.Section)) {
				return true
			}
			continue
		}
		if !strings.Contains(strings.ToLower(h.Path), strings.ToLower(r.Path)) {
			continue
		}
		if len(r.Pages) == 0 || slices.Contains(r.Pages, h.Page) {
			return true
		}
	}
	return false
}

func search(addr string, q *query, mode string, limit, norm int, boost float64, boostSet bool, extra url.Values) ([]corpus.Hit, error) {
	params := url.Values{}
	for key, vals := range extra {
		params[key] = vals
	}
	params.Set("q", q.Query)
	params.Set("kind", q.Kind)
	params.Set("mode", mode)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("norm", strconv.Itoa(norm))
	if boostSet {
		params.Set("title_boost", strconv.FormatFloat(boost, 'f', -1, 64))
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		addr+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}

	var body struct {
		Hits []corpus.Hit `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Hits, nil
}
