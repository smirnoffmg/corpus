// Command evalgen writes a judged query set for the vault from the vault's own
// structure: aliases, link display texts and links to headings. The set is
// private — its queries are the vault's words — so it is written beside the
// library, and cmd/eval reads it with -queries.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/smirnoffmg/corpus/internal/vaultpairs"
)

type judgement struct {
	Note    string `json:"note"`
	Section string `json:"section,omitempty"`
}

type query struct {
	ID       string      `json:"id"`
	Style    string      `json:"style"`
	Kind     string      `json:"kind"`
	Query    string      `json:"query"`
	Share    float64     `json:"share"`
	Relevant []judgement `json:"relevant"`
}

func main() {
	vault := flag.String("vault", os.Getenv("VAULT_DIR"), "the Obsidian vault")
	out := flag.String("out", defaultOut(), "where to write the set")
	// About 50 topics are needed before a 5% difference predicts the better
	// system (Voorhees and Buckley, in Sanderson, p. 314); a source with fewer
	// pairs than this is used whole.
	perSource := flag.Int("n", 100, "pairs to keep from each source")
	flag.Parse()
	if *vault == "" {
		log.Fatal("no vault: pass -vault or set VAULT_DIR")
	}

	pairs, err := vaultpairs.Collect(*vault)
	if err != nil {
		log.Fatal(err)
	}
	found := map[string]int{}
	for _, p := range pairs {
		found[p.Source]++
	}

	kept := vaultpairs.Sample(pairs, *perSource)
	set := make([]query, 0, len(kept))
	styles := map[string]int{}
	for _, p := range kept {
		// Copied means every content word of the query is in the target, which
		// is what lets the text leg find it at all.
		style := p.Source + "-rephrased"
		if p.Share == 1 {
			style = p.Source + "-copied"
		}
		styles[style]++
		sum := sha256.Sum256([]byte(p.Source + "\x00" + p.Query + "\x00" + p.Note + "\x00" + p.Section))
		set = append(set, query{
			ID:       p.Source + ":" + hex.EncodeToString(sum[:4]),
			Style:    style,
			Kind:     "vault",
			Query:    p.Query,
			Share:    p.Share,
			Relevant: []judgement{{Note: p.Note, Section: p.Section}},
		})
	}

	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pairs found: alias %d, link %d, section %d\n", found["alias"], found["link"], found["section"])
	fmt.Printf("kept %d in %s:\n", len(set), *out)
	for _, s := range []string{"alias-copied", "alias-rephrased", "link-copied", "link-rephrased", "section-copied", "section-rephrased"} {
		fmt.Printf("  %-18s %d\n", s, styles[s])
	}
}

func defaultOut() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "vault.json"
	}
	return filepath.Join(home, ".corpus", "eval", "vault.json")
}
