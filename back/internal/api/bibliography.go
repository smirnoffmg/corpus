package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/smirnoffmg/corpus/internal/cite"
	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Bibliography is where descriptions and added citation styles are kept.
type Bibliography interface {
	ReferenceKey(ctx context.Context, kind, path string) (string, error)
	Reference(ctx context.Context, key string) (corpus.Reference, error)
	References(ctx context.Context) ([]corpus.Reference, error)
	SaveReference(ctx context.Context, key string, csl corpus.CSL, status string) (corpus.Reference, error)
	Styles(ctx context.Context) ([]corpus.Style, error)
	StyleXML(ctx context.Context, id string) (string, error)
	SaveStyle(ctx context.Context, id, title, xml string) error
}

// Lookup fills a description from an identifier, over the network.
type Lookup interface {
	DOI(ctx context.Context, doi string) (corpus.CSL, error)
	ISBN(ctx context.Context, isbn string) (corpus.CSL, error)
	Style(ctx context.Context, name string) (id, title, xml string, err error)
}

// WithBibliography serves descriptions, export and citation styles; lookup may
// be nil, and then only the network-bound parts answer as unavailable.
func WithBibliography(b Bibliography, lookup Lookup) Option {
	return func(s *Service) { s.bibliography, s.lookup = b, lookup }
}

// errNoBibliography keeps a mis-wired server from answering 404 as if every
// source simply had no description.
var errNoBibliography = errors.New("bibliography is not configured on this server")

func (s *Service) bibliographyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /bibliography", s.withBibliography(s.getReference))
	mux.HandleFunc("PUT /bibliography", s.withBibliography(s.putReference))
	mux.HandleFunc("POST /bibliography/lookup", s.withBibliography(s.lookupReference))
	mux.HandleFunc("GET /bibliography/export", s.withBibliography(s.exportReferences))
	mux.HandleFunc("GET /styles", s.withBibliography(s.listStyles))
	mux.HandleFunc("GET /styles/{id}", s.withBibliography(s.getStyle))
	mux.HandleFunc("POST /styles", s.withBibliography(s.addStyle))
	mux.HandleFunc("POST /styles/fetch", s.withBibliography(s.fetchStyle))
}

func (s *Service) withBibliography(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.bibliography == nil {
			http.Error(w, errNoBibliography.Error(), http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
}

func (s *Service) referenceKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	q := r.URL.Query()
	key, err := s.bibliography.ReferenceKey(r.Context(), q.Get("kind"), q.Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return "", false
	}
	return key, true
}

// getReference answers with the key even when there is no description yet,
// so the form knows where a new one will be filed.
func (s *Service) getReference(w http.ResponseWriter, r *http.Request) {
	key, ok := s.referenceKey(w, r)
	if !ok {
		return
	}
	ref, err := s.bibliography.Reference(r.Context(), key)
	out := map[string]any{"key": key, "reference": nil}
	switch {
	case err == nil:
		out["reference"] = ref
	case !errors.Is(err, corpus.ErrNoReference):
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

func (s *Service) putReference(w http.ResponseWriter, r *http.Request) {
	key, ok := s.referenceKey(w, r)
	if !ok {
		return
	}
	var in struct {
		CSL    corpus.CSL `json:"csl"`
		Status string     `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || in.CSL == nil {
		http.Error(w, "expected {csl, status}", http.StatusBadRequest)
		return
	}
	if in.Status != "draft" && in.Status != "checked" {
		http.Error(w, "status must be draft or checked", http.StatusBadRequest)
		return
	}
	ref, err := s.bibliography.SaveReference(r.Context(), key, in.CSL, in.Status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ref)
}

func (s *Service) lookupReference(w http.ResponseWriter, r *http.Request) {
	if s.lookup == nil {
		http.Error(w, "lookups are not configured on this server", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		DOI  string `json:"doi"`
		ISBN string `json:"isbn"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || (in.DOI == "") == (in.ISBN == "") {
		http.Error(w, "expected exactly one of doi or isbn", http.StatusBadRequest)
		return
	}
	var csl corpus.CSL
	var err error
	if in.DOI != "" {
		csl, err = s.lookup.DOI(r.Context(), in.DOI)
	} else {
		csl, err = s.lookup.ISBN(r.Context(), in.ISBN)
	}
	if err != nil {
		lookupError(w, err)
		return
	}
	writeJSON(w, map[string]corpus.CSL{"csl": csl})
}

// lookupError tells "the service has no such record" from "the service could
// not be reached", which offline is the usual case and needs a different fix.
func lookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, cite.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, "lookup failed — is there a network connection? "+err.Error(), http.StatusBadGateway)
}

func (s *Service) exportReferences(w http.ResponseWriter, r *http.Request) {
	refs, err := s.bibliography.References(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch r.URL.Query().Get("format") {
	case "biblatex":
		w.Header().Set("Content-Type", "application/x-bibtex; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="corpus.bib"`)
		_, _ = io.WriteString(w, cite.BibLaTeX(refs))
	case "csl-json":
		items := make([]corpus.CSL, 0, len(refs))
		for _, ref := range refs {
			item := corpus.CSL{}
			for k, v := range ref.CSL {
				item[k] = v
			}
			item["id"] = ref.CiteKey
			items = append(items, item)
		}
		w.Header().Set("Content-Disposition", `attachment; filename="corpus.json"`)
		writeJSON(w, items)
	default:
		http.Error(w, "format must be biblatex or csl-json", http.StatusBadRequest)
	}
}

func (s *Service) listStyles(w http.ResponseWriter, r *http.Request) {
	styles, err := s.bibliography.Styles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if styles == nil {
		styles = []corpus.Style{}
	}
	writeJSON(w, map[string][]corpus.Style{"styles": styles})
}

func (s *Service) getStyle(w http.ResponseWriter, r *http.Request) {
	xml, err := s.bibliography.StyleXML(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = io.WriteString(w, xml)
}

func (s *Service) addStyle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := cite.ParseStyle(string(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if info.Parent != "" {
		http.Error(w, "this is a dependent style; add it by name so its parent is fetched", http.StatusBadRequest)
		return
	}
	s.saveStyle(w, r, cite.StyleSlug(info.ID), info.Title, string(body))
}

func (s *Service) fetchStyle(w http.ResponseWriter, r *http.Request) {
	if s.lookup == nil {
		http.Error(w, "lookups are not configured on this server", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || in.Name == "" {
		http.Error(w, "expected {name}", http.StatusBadRequest)
		return
	}
	id, title, xml, err := s.lookup.Style(r.Context(), in.Name)
	if err != nil {
		lookupError(w, err)
		return
	}
	s.saveStyle(w, r, id, title, xml)
}

func (s *Service) saveStyle(w http.ResponseWriter, r *http.Request, id, title, xml string) {
	if err := s.bibliography.SaveStyle(r.Context(), id, title, xml); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, corpus.Style{ID: id, Title: title})
}
