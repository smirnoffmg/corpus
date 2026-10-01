package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smirnoffmg/corpus/internal/corpus"
)

// Beat overwrites the indexer's last beat, stamped by the database's clock.
func (s *Store) Beat(ctx context.Context, b *corpus.Beat) error {
	var lastPass *time.Time
	if !b.LastPass.IsZero() {
		lastPass = &b.LastPass
	}
	settings := b.Settings
	if settings == nil {
		settings = map[string]string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO indexer_beat (id, at, phase, phase_from, started, last_pass, last_took, last_error, pause, settings)
		VALUES (true, now(), $1, $2, $3, $4, make_interval(secs => $5), $6, make_interval(secs => $7), $8)
		ON CONFLICT (id) DO UPDATE SET
		    at = now(), phase = $1, phase_from = $2, started = $3, last_pass = $4,
		    last_took = make_interval(secs => $5), last_error = $6, pause = make_interval(secs => $7), settings = $8`,
		b.Phase, b.PhaseFrom, b.Started, lastPass, b.LastTook.Seconds(), b.LastError, b.Interval.Seconds(), settings)
	return err
}

// Pulse reads the indexer's last beat; found is false until it has beaten once.
func (s *Store) Pulse(ctx context.Context) (p corpus.Pulse, found bool, err error) {
	var lastPass *time.Time
	var took, pause float64
	err = s.pool.QueryRow(ctx, `
		SELECT at, now(), phase, phase_from, started, last_pass,
		       coalesce(extract(epoch FROM last_took), 0)::float8, last_error,
		       extract(epoch FROM pause)::float8, settings
		FROM indexer_beat`).Scan(
		&p.At, &p.Now, &p.Phase, &p.PhaseFrom, &p.Started, &lastPass,
		&took, &p.LastError, &pause, &p.Settings)
	if errors.Is(err, pgx.ErrNoRows) {
		return corpus.Pulse{}, false, nil
	}
	if err != nil {
		return corpus.Pulse{}, false, err
	}
	if lastPass != nil {
		p.LastPass = *lastPass
	}
	p.LastTook = time.Duration(took * float64(time.Second))
	p.Interval = time.Duration(pause * float64(time.Second))
	return p, true, nil
}
