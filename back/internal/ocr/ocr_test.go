package ocr_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smirnoffmg/corpus/internal/ocr"
)

// fakeEngine "recognises" a page as its number, and records which pages it was
// asked for.
type fakeEngine struct {
	mu      sync.Mutex
	asked   []int
	failOn  int
	delay   time.Duration
	running atomic.Int32
	peak    atomic.Int32
}

func (f *fakeEngine) Pages(context.Context, string) (int, error) { return 0, nil }

func (f *fakeEngine) Recognize(ctx context.Context, _ string, page int) (string, error) {
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	f.mu.Lock()
	f.asked = append(f.asked, page)
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if page == f.failOn {
		return "", errors.New("tesseract: cannot read page")
	}
	return fmt.Sprintf("страница %d", page), nil
}

const hash = "0123abcd"

func TestRecognizeCachesEveryPageAndLoadsThemInOrder(t *testing.T) {
	cache := ocr.Cache{Dir: t.TempDir()}
	engine := &fakeEngine{delay: time.Millisecond}
	var last atomic.Int64
	svc := ocr.Service{Engine: engine, Cache: cache, Workers: 3}

	complete, err := svc.Recognize(context.Background(), hash, "book.pdf", 7, nil, func(n int) { last.Store(int64(n)) })
	require.NoError(t, err)
	require.True(t, complete)
	require.EqualValues(t, 7, last.Load(), "progress ends at every page recognised")
	require.LessOrEqual(t, engine.peak.Load(), int32(3), "never more pages at once than workers")
	require.Greater(t, engine.peak.Load(), int32(1), "and more than one when there are workers for it")

	pages, ok := svc.Cached(hash, 7)
	require.True(t, ok)
	require.Equal(t, "страница 1", pages[0])
	require.Equal(t, "страница 7", pages[6])
}

// Recognising a big scan takes an hour; a restart must not start it over.
func TestRecognizeResumesFromTheCache(t *testing.T) {
	cache := ocr.Cache{Dir: t.TempDir()}
	for _, p := range []int{1, 2, 4} {
		require.NoError(t, cache.Save(hash, p, fmt.Sprintf("страница %d", p)))
	}
	engine := &fakeEngine{}
	svc := ocr.Service{Engine: engine, Cache: cache, Workers: 2}

	complete, err := svc.Recognize(context.Background(), hash, "book.pdf", 5, nil, nil)
	require.NoError(t, err)
	require.True(t, complete)
	require.ElementsMatch(t, []int{3, 5}, engine.asked)
}

func TestAnIncompleteCacheIsNotABook(t *testing.T) {
	cache := ocr.Cache{Dir: t.TempDir()}
	require.NoError(t, cache.Save(hash, 1, "первая"))
	_, ok := (ocr.Service{Cache: cache}).Cached(hash, 2)
	require.False(t, ok)

	// A page recognised as blank is still a recognised page.
	require.NoError(t, cache.Save(hash, 2, ""))
	pages, ok := (ocr.Service{Cache: cache}).Cached(hash, 2)
	require.True(t, ok)
	require.Equal(t, []string{"первая", ""}, pages)
}

// An upload arriving mid-book must not wait for the book: recognition stops
// between pages, keeping what it has.
func TestRecognizeGivesWayWhenAskedToStop(t *testing.T) {
	cache := ocr.Cache{Dir: t.TempDir()}
	engine := &fakeEngine{delay: 5 * time.Millisecond}
	stop := make(chan struct{}, 1)
	svc := ocr.Service{Engine: engine, Cache: cache, Workers: 1}

	var seen atomic.Int32
	complete, err := svc.Recognize(context.Background(), hash, "book.pdf", 100, stop, func(n int) {
		if seen.Add(1) == 3 {
			stop <- struct{}{}
		}
	})
	require.NoError(t, err)
	require.False(t, complete)
	require.Less(t, len(engine.asked), 10, "stopped soon after being asked")

	missing := cache.Missing(hash, 100)
	require.Len(t, missing, 100-len(engine.asked), "every page recognised before stopping is kept")
}

func TestAPageThatFailsStopsTheBookAndKeepsTheRest(t *testing.T) {
	cache := ocr.Cache{Dir: t.TempDir()}
	engine := &fakeEngine{failOn: 3}
	svc := ocr.Service{Engine: engine, Cache: cache, Workers: 1}

	complete, err := svc.Recognize(context.Background(), hash, "book.pdf", 5, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "page 3")
	require.False(t, complete)
	require.NotContains(t, cache.Missing(hash, 5), 1)
	require.NotContains(t, cache.Missing(hash, 5), 2)
}

func TestTheCacheKeyIsAHashNotAPath(t *testing.T) {
	cache := ocr.Cache{Dir: t.TempDir()}
	require.Error(t, cache.Save("../escape", 1, "x"))
	require.Error(t, cache.Save("", 1, "x"))
	entries, err := os.ReadDir(filepath.Dir(cache.Dir))
	require.NoError(t, err)
	for _, e := range entries {
		require.NotEqual(t, "escape", e.Name())
	}
}
