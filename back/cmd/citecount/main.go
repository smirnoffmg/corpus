// Command citecount counts, week by week, the citations of the library in the
// vault's notes. It reads the vault's git history, which the Obsidian backup
// commits every few minutes, so each week is the vault as it stood at its end,
// and the titles of the books and papers from the running server.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/smirnoffmg/corpus/internal/vaultcites"
)

// notes is every note but the templates, whose example citations cite nothing,
// and the dot directories, which hold settings and commands rather than notes.
var notes = []string{"*.md", ":(exclude)99 - templates", ":(exclude,glob).*/**", ":(exclude,glob)**/.*/**"}

func main() {
	vault := flag.String("vault", os.Getenv("VAULT_DIR"), "the Obsidian vault, a git repository")
	addr := flag.String("addr", "http://localhost:8080", "corpus server, for the library's titles")
	weeks := flag.Int("weeks", 12, "weeks to go back")
	flag.Parse()
	if *vault == "" {
		log.Fatal("no vault: pass -vault or set VAULT_DIR")
	}
	ctx := context.Background()

	library, err := titles(ctx, *addr)
	if err != nil {
		log.Fatal(err)
	}

	type row struct {
		end   time.Time
		tally vaultcites.Tally
	}
	var rows []row
	now := time.Now()
	for i := range *weeks + 1 {
		end := now.AddDate(0, 0, -7*i)
		rev, err := git(ctx, *vault, "rev-list", "-1", "--before="+end.Format(time.RFC3339), "HEAD")
		if err != nil {
			log.Fatal(err)
		}
		rev = strings.TrimSpace(rev)
		if rev == "" {
			break // the history starts after this week
		}
		lines, err := linesAt(ctx, *vault, rev)
		if err != nil {
			log.Fatal(err)
		}
		rows = append(rows, row{end, vaultcites.Count(lines, library)})
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "week to\tcitations\tof the library\tnew\tbooks cited\tnotes citing\t")
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		added := ""
		if i+1 < len(rows) {
			added = fmt.Sprintf("%+d", r.tally.Library-rows[i+1].tally.Library)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%d\t%d\t\n", r.end.Format(time.DateOnly),
			r.tally.Citations, r.tally.Library, added, r.tally.Sources, r.tally.Notes)
	}
	if err := w.Flush(); err != nil {
		log.Fatal(err)
	}
}

// linesAt is every line with an asterisk in the notes at rev; the citation
// pattern is matched here rather than by git grep, whose regex dialect varies
// with how git was built.
func linesAt(ctx context.Context, vault, rev string) ([]vaultcites.Line, error) {
	args := append([]string{"grep", "-I", "--null", "-F", "-e", "*", rev, "--"}, notes...)
	out, err := git(ctx, vault, args...)
	if err != nil {
		return nil, err
	}
	var lines []vaultcites.Line
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		name, text, ok := strings.Cut(sc.Text(), "\x00")
		if !ok {
			continue
		}
		lines = append(lines, vaultcites.Line{Note: strings.TrimPrefix(name, rev+":"), Text: text})
	}
	return lines, sc.Err()
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	// git grep exits 1 when nothing matched, which is a week with no citations.
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) && exit.ExitCode() == 1 && args[0] == "grep" {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func titles(ctx context.Context, addr string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/sources", http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/sources: %s", addr, resp.Status)
	}
	var body struct {
		Sources []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	var out []string
	for _, s := range body.Sources {
		if s.Kind == "book" || s.Kind == "paper" {
			out = append(out, s.Title)
		}
	}
	return out, nil
}
