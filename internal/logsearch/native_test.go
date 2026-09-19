package logsearch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/logbuffer"
)

func testEntries() []logbuffer.Entry {
	base := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)
	return []logbuffer.Entry{
		{At: base, Message: `[GIN] 2026/09/19 - 04:00:00 | 200 | 12.3ms | 192.168.10.11 | GET "/v1/movies/JavBus/SSIS-001" trace=trace-aaaa-1111`},
		{At: base.Add(time.Second), Message: `[TRACE] 2026/09/19 04:00:01 pruned 3 traces trace=trace-aaaa-1111`},
		{At: base.Add(2 * time.Second), Message: `[GIN] 2026/09/19 - 04:00:02 | 500 | 30ms | 192.168.10.11 | GET "/v1/actors/Gfriends/Saeki" trace=trace-bbbb-2222`},
		{At: base.Add(3 * time.Second), Message: `[GORM] 2026/09/19 04:00:03 record not found`},
	}
}

func nativeBackend() *NativeBackend {
	return &NativeBackend{Entries: func(int) []logbuffer.Entry { return testEntries() }}
}

func TestNativeBackendFilters(t *testing.T) {
	backend := nativeBackend()
	ctx := context.Background()

	// Trace-scoped search matches only the requested trace, exactly.
	result := backend.Search(ctx, Query{TraceIDs: []string{"trace-aaaa-1111"}})
	assert.Equal(t, "ok", result.Status)
	require.Len(t, result.Lines, 2)
	for _, line := range result.Lines {
		assert.Equal(t, SourceNative, line.Source)
		assert.Equal(t, "NATIVE", line.Badge)
		assert.Equal(t, "trace-aaaa-1111", line.TraceID)
	}

	// A prefix must not match: IDs are matched exactly.
	partial := backend.Search(ctx, Query{TraceIDs: []string{"trace-aaaa"}})
	assert.Empty(t, partial.Lines)

	// Pattern metacharacters in the query are escaped, not interpreted.
	hostile := backend.Search(ctx, Query{TraceIDs: []string{"trace-aaaa-1111"}, Text: ".*"})
	assert.Empty(t, hostile.Lines, "regex metacharacters must be treated as literal text")

	// Level, component, provider and text filters.
	errors := backend.Search(ctx, Query{Level: "error"})
	require.Len(t, errors.Lines, 1)
	assert.Contains(t, errors.Lines[0].Message, "500")

	gorm := backend.Search(ctx, Query{Component: "database"})
	require.Len(t, gorm.Lines, 1)
	assert.Contains(t, gorm.Lines[0].Message, "GORM")

	provider := backend.Search(ctx, Query{Provider: "JavBus"})
	require.Len(t, provider.Lines, 1)
	assert.Contains(t, provider.Lines[0].Message, "SSIS-001")

	text := backend.Search(ctx, Query{Text: "SSIS-001"})
	require.Len(t, text.Lines, 1)

	// Time windows are honoured (the window is inclusive on both ends).
	since := time.Date(2026, 9, 19, 4, 0, 2, 0, time.UTC)
	until := time.Date(2026, 9, 19, 4, 0, 2, 500*int(time.Millisecond), time.UTC)
	window := backend.Search(ctx, Query{Since: &since, Until: &until})
	require.Len(t, window.Lines, 1)
	assert.Contains(t, window.Lines[0].Message, "trace-bbbb-2222")
}

func TestNativeBackendLimitsAndTruncation(t *testing.T) {
	backend := nativeBackend()

	limited := backend.Search(context.Background(), Query{Limit: 1})
	assert.Len(t, limited.Lines, 1)
	assert.True(t, limited.Truncated, "more matches than the limit must be flagged")
	assert.Equal(t, 4, limited.MatchCount)
	assert.Equal(t, NativeRetention, limited.Retention)

	huge := backend.Search(context.Background(), Query{Limit: 100000})
	assert.Equal(t, MaxSearchLimit, huge.Effective.Limit)
}

func TestQueryNormalizeBoundsAndSanitizes(t *testing.T) {
	since := time.Now()
	until := since.Add(-time.Hour)
	query := Query{
		TraceIDs: []string{"trace-aaaa-1111", "trace-aaaa-1111", "bad id!", "trace-bbbb-2222"},
		Text:     "  spaced  ",
		Level:    " ERROR ",
		Since:    &since,
		Until:    &until,
		Limit:    5000,
	}.Normalize()

	assert.Equal(t, []string{"trace-aaaa-1111", "trace-bbbb-2222"}, query.TraceIDs,
		"duplicates and malformed IDs are dropped")
	assert.Equal(t, "spaced", query.Text)
	assert.Equal(t, "error", query.Level)
	assert.Equal(t, MaxSearchLimit, query.Limit)
	assert.True(t, query.Since.Before(*query.Until), "a reversed range is corrected")
}
