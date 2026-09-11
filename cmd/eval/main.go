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
	"slices"
	"strconv"
	"strings"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

type judgement struct {
	Path  string `json:"path"`
	Pages []int  `json:"pages"`
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
	path := flag.String("queries", "eval/queries.json", "judged query set")
	limit := flag.Int("limit", 10, "hits to request per query")
	verbose := flag.Bool("v", false, "list the queries each mode misses")
	norm := flag.Int("norm", 0, "ts_rank_cd length normalisation bit mask to measure")
	flag.Parse()

	raw, err := os.ReadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	var queries []query
	if err := json.Unmarshal(raw, &queries); err != nil {
		log.Fatal(err)
	}

	modes := []string{"fts", "vector", "hybrid"}
	styles := []string{"exact", "paraphrase"}

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

	for _, q := range queries {
		for _, mode := range modes {
			hits, err := search(*addr, &q, mode, *limit, *norm)
			if err != nil {
				log.Fatalf("%s/%s: %v", q.ID, mode, err)
			}
			s := measure(&q, hits)
			add(overall[mode], s)
			add(byStyle[mode][q.Style], s)
			if s.found == 0 {
				misses[mode] = append(misses[mode], q.ID)
			}
		}
	}

	fmt.Printf("%d queries (%d exact, %d paraphrase)\n\n", len(queries),
		count(queries, "exact"), count(queries, "paraphrase"))
	fmt.Printf("%-8s %-12s %7s %7s %8s\n", "mode", "queries", "MRR", "P@5", "found@10")
	for _, mode := range modes {
		report(mode, "all", overall[mode])
		for _, st := range styles {
			report("", st, byStyle[mode][st])
		}
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

func report(mode, label string, s *score) {
	if s.queries == 0 {
		return
	}
	fmt.Printf("%-8s %-12s %7.3f %7.3f %7.0f%%\n", mode, label, s.mrr(), s.p5(), s.hit()*100)
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
		if !strings.Contains(strings.ToLower(h.Path), strings.ToLower(r.Path)) {
			continue
		}
		if len(r.Pages) == 0 || slices.Contains(r.Pages, h.Page) {
			return true
		}
	}
	return false
}

func search(addr string, q *query, mode string, limit, norm int) ([]corpus.Hit, error) {
	params := url.Values{}
	params.Set("q", q.Query)
	params.Set("kind", q.Kind)
	params.Set("mode", mode)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("norm", strconv.Itoa(norm))

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

func count(queries []query, style string) int {
	n := 0
	for _, q := range queries {
		if q.Style == style {
			n++
		}
	}
	return n
}
