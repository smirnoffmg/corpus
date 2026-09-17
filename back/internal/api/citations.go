package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Citations is a publication's list of references and the graph it makes with
// the rest of the corpus.
type Citations interface {
	SourceHash(ctx context.Context, path string) (string, bool, error)
	Citations(ctx context.Context, paper string) ([]corpus.Citation, error)
	Citing(ctx context.Context, key, fingerprint string) ([]corpus.CitingPaper, error)
	SharedCitations(ctx context.Context, a, b string) ([]corpus.Citation, error)
	UpdateCitations(ctx context.Context, paper string, cs []corpus.Citation) error
}

// WithCitations serves what publications cite.
func WithCitations(c Citations) Option {
	return func(s *Service) { s.citations = c }
}

var errNoCitations = errors.New("citations are not configured on this server")

type citationsInput struct {
	Path      string `json:"path" jsonschema:"path of a source, exactly as corpus_search returned it"`
	Direction string `json:"direction,omitempty" jsonschema:"'cited' (default) for the works this publication cites, 'citing' for the publications in the corpus that cite this source"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum entries to return, default 50"`
}

type citationsOutput struct {
	Path      string               `json:"path"`
	Cited     []corpus.Citation    `json:"cited,omitempty"`
	Citing    []corpus.CitingPaper `json:"citing,omitempty"`
	Truncated bool                 `json:"truncated,omitempty" jsonschema:"set when there were more entries than the limit"`
}

// defaultCitationLimit is a whole reference list for most papers; a long one is
// cut rather than turned into a wall of text.
const defaultCitationLimit = 50

// citations answers the MCP tool: one tool for both directions, because two
// nearly identical tools over one table are a thing to confuse rather than a
// thing to choose between.
func (s *Service) citationsTool(ctx context.Context, in citationsInput) (citationsOutput, error) {
	if s.citations == nil {
		return citationsOutput{}, errNoCitations
	}
	hash, found, err := s.citations.SourceHash(ctx, in.Path)
	if err != nil {
		return citationsOutput{}, err
	}
	if !found {
		return citationsOutput{}, fmt.Errorf("nothing is indexed under %s", in.Path)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultCitationLimit
	}
	out := citationsOutput{Path: in.Path}
	if in.Direction == "citing" {
		papers, citingErr := s.citations.Citing(ctx, hash, "")
		if citingErr != nil {
			return citationsOutput{}, citingErr
		}
		out.Citing, out.Truncated = cut(papers, limit)
		return out, nil
	}
	entries, err := s.citations.Citations(ctx, hash)
	if err != nil {
		return citationsOutput{}, err
	}
	out.Cited, out.Truncated = cut(entries, limit)
	return out, nil
}

func cut[T any](items []T, limit int) ([]T, bool) {
	if len(items) <= limit {
		return items, false
	}
	return items[:limit], true
}

func (s *Service) citationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /citations", s.withCitations(s.listCitations))
	mux.HandleFunc("GET /citations/citing", s.withCitations(s.listCiting))
	mux.HandleFunc("GET /citations/shared", s.withCitations(s.listShared))
	mux.HandleFunc("POST /citations/enrich", s.withCitations(s.enrichCitations))
}

func (s *Service) withCitations(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.citations == nil {
			http.Error(w, errNoCitations.Error(), http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
}

// hashOf turns a path into what the corpus files a file's work under. A path
// nothing is indexed under is a 404 rather than an empty list: "this paper
// cites nothing" and "there is no such paper" are different answers.
func (s *Service) hashOf(w http.ResponseWriter, r *http.Request, path string) (string, bool) {
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return "", false
	}
	hash, found, err := s.citations.SourceHash(r.Context(), path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return "", false
	}
	if !found {
		http.Error(w, "nothing is indexed under "+path, http.StatusNotFound)
		return "", false
	}
	return hash, true
}

func (s *Service) listCitations(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	hash, ok := s.hashOf(w, r, path)
	if !ok {
		return
	}
	entries, err := s.citations.Citations(r.Context(), hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"path": path, "citations": entries})
}

// listCiting answers the other direction. A path names a work the library
// holds, and resolution has already pointed the entries at it; a DOI names one
// it may not hold at all, and then the entries themselves are what agree.
func (s *Service) listCiting(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var key, fingerprint string
	switch {
	case q.Get("path") != "":
		var ok bool
		if key, ok = s.hashOf(w, r, q.Get("path")); !ok {
			return
		}
	case q.Get("doi") != "":
		fingerprint = strings.ToLower(strings.TrimSpace(q.Get("doi")))
	default:
		http.Error(w, "path or doi is required", http.StatusBadRequest)
		return
	}

	papers, err := s.citations.Citing(r.Context(), key, fingerprint)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"citing": papers})
}

func (s *Service) listShared(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a, ok := s.hashOf(w, r, q.Get("a"))
	if !ok {
		return
	}
	b, ok := s.hashOf(w, r, q.Get("b"))
	if !ok {
		return
	}
	shared, err := s.citations.SharedCitations(r.Context(), a, b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"citations": shared})
}

// enrichLimit bounds one request. doi.org is someone else's service, and a
// reference list of two hundred entries is not a reason to hammer it.
const enrichLimit = 50

// enrichCitations fills in what the registry knows about the entries that named
// a DOI and little else. It is a request of its own and never part of a pass:
// the corpus holds a diary and reads nothing to anyone unless it is asked.
func (s *Service) enrichCitations(w http.ResponseWriter, r *http.Request) {
	if s.lookup == nil {
		http.Error(w, "lookups are not configured on this server", http.StatusServiceUnavailable)
		return
	}
	hash, ok := s.hashOf(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	entries, err := s.citations.Citations(r.Context(), hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filled, asked := 0, 0
	for i := range entries {
		if entries[i].DOI == "" || !thin(&entries[i]) {
			continue
		}
		if asked >= enrichLimit {
			break
		}
		asked++
		record, lookupErr := s.lookup.DOI(r.Context(), entries[i].DOI)
		if lookupErr != nil {
			slog.InfoContext(r.Context(), "no registry record for a citation",
				"doi", entries[i].DOI, "err", lookupErr)
			continue
		}
		if applyCSL(&entries[i], record) {
			filled++
		}
	}
	if filled > 0 {
		if err := s.citations.UpdateCitations(r.Context(), hash, entries); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]any{"asked": asked, "filled": filled, "citations": entries})
}

// thin is an entry the parser could not read much out of. One that already has
// its title, its authors and its year is not worth a request.
func thin(c *corpus.Citation) bool {
	return c.Title == "" || c.Authors == "" || c.Year == 0
}

// applyCSL takes from the registry only what is missing: a reading from the
// page may be better than nothing, and it is what the reader checked.
func applyCSL(c *corpus.Citation, record corpus.CSL) bool {
	before := *c
	if c.Title == "" {
		c.Title = cslString(record["title"])
	}
	if c.Container == "" {
		c.Container = cslString(record["container-title"])
	}
	if c.Authors == "" {
		c.Authors = cite.AuthorNames(record)
	}
	if c.Year == 0 {
		c.Year = cite.Year(record)
	}
	return *c != before
}

func cslString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
