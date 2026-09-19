package logsearch

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// Searcher runs the configured backends and returns their results side by side,
// one status per source, so a slow or broken backend can never fail the others.
type Searcher struct {
	native  *NativeBackend
	graylog *GraylogBackend
	cfg     GraylogConfig
}

// New builds a searcher from the environment.
func New(cfg GraylogConfig) *Searcher {
	cfg = cfg.WithDefaults()
	return &Searcher{
		native:  NewNativeBackend(),
		graylog: NewGraylogBackend(cfg),
		cfg:     cfg,
	}
}

// GraylogConfig exposes the effective configuration for the stats endpoint.
func (s *Searcher) GraylogConfig() GraylogConfig { return s.cfg }

// Search runs the requested sources. Sources are searched concurrently and every
// result is returned, even when another fails.
func (s *Searcher) Search(ctx context.Context, query Query, sources []Source) []Result {
	query = query.Normalize()
	if len(sources) == 0 {
		sources = []Source{SourceNative, SourceGraylog}
	}

	requested := make(map[Source]bool, len(sources))
	for _, source := range sources {
		requested[source] = true
	}

	type indexed struct {
		index  int
		result Result
	}
	results := make([]Result, 0, 2)
	channel := make(chan indexed, 2)
	var wait sync.WaitGroup

	launch := func(index int, backend Backend) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			channel <- indexed{index: index, result: backend.Search(ctx, query)}
		}()
	}
	if requested[SourceNative] {
		launch(0, s.native)
	}
	if requested[SourceGraylog] {
		launch(1, s.graylog)
	}
	wait.Wait()
	close(channel)
	for item := range channel {
		results = append(results, item.result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return sourceOrder(results[i].Source) < sourceOrder(results[j].Source)
	})
	return results
}

func sourceOrder(source Source) int {
	switch source {
	case SourceTrace:
		return 0
	case SourceNative:
		return 1
	case SourceGraylog:
		return 2
	}
	return 3
}

// Deduplicate collapses visually identical Native/Graylog lines, keeping one
// representative and reporting how many originals were merged. The durable
// backend wins the tie so an ephemeral buffer entry is never preferred over a
// durable one. Structured trace events are never deduplicated, because they are
// not raw log lines.
func Deduplicate(lines []Line) (kept []Line, hidden map[string]int) {
	kept = make([]Line, 0, len(lines))
	hidden = make(map[string]int)
	for _, line := range lines {
		if line.Source == SourceTrace || line.Fingerprint == "" {
			kept = append(kept, line)
			continue
		}
		duplicate := -1
		for index, existing := range kept {
			if existing.Source == SourceTrace || existing.Fingerprint != line.Fingerprint {
				continue
			}
			if existing.Source == line.Source {
				// Same backend repeating a line is a real repetition: keep it.
				continue
			}
			duplicate = index
			break
		}
		if duplicate < 0 {
			kept = append(kept, line)
			continue
		}
		hidden[line.Fingerprint]++
		// Replace an ephemeral representative with the durable copy.
		if kept[duplicate].Source == SourceNative && line.Source != SourceNative {
			kept[duplicate] = line
		}
	}
	return kept, hidden
}

// TraceLines renders structured trace events as timeline lines. They always
// carry the TRACE badge and are never merged with raw logs.
func TraceLines(events []trace.Event) []Line {
	lines := make([]Line, 0, len(events))
	for _, event := range events {
		lines = append(lines, Line{
			Source:    SourceTrace,
			Badge:     SourceBadge(SourceTrace),
			At:        event.At,
			Level:     event.Level,
			Component: event.Component,
			Stage:     event.Stage,
			Provider:  event.Provider,
			TraceID:   event.TraceID,
			Message:   strings.TrimSpace(event.Message),
		})
	}
	return lines
}
