package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T, mutate func(cfg *Config)) *Service {
	t.Helper()
	cfg := Config{
		Enabled:         true,
		DSN:             filepath.Join(t.TempDir(), "traces.db"),
		RetentionDays:   DefaultRetentionDays,
		MaxRuns:         DefaultMaxRuns,
		MaxEventsPerRun: DefaultMaxEventsPerRun,
		PruneInterval:   MinPruneInterval,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	service := NewService(cfg)
	require.True(t, service.Enabled(), "test service should open its store")
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func TestVideoAndActorTracesStaySeparate(t *testing.T) {
	service := newTestService(t, nil)

	video, ok := service.Start(StartInput{
		Kind:       KindVideo,
		Operation:  OperationIdentify,
		Query:      "SSIS-001",
		ClientName: "emby-plugin",
		ClientIP:   "192.168.10.151",
	})
	require.True(t, ok)
	assert.True(t, video.Created())
	video.Event(Event{Component: ComponentMetaTube, Stage: StageRequestReceived, Message: "video lookup"})
	videoResultCount := 1
	video.Finish(FinishInput{Status: StatusSucceeded, SelectedProvider: "JavBus", SelectedProviderID: "SSIS-001", ResultCount: &videoResultCount})

	actor, ok := service.Start(StartInput{
		Kind:       KindActor,
		Operation:  OperationIdentify,
		Query:      "Saeki Yumika",
		ClientName: "emby-plugin",
	})
	require.True(t, ok)
	actor.Event(Event{Component: ComponentMetaTube, Stage: StageRequestReceived, Message: "actor lookup"})
	actor.Finish(FinishInput{Status: StatusPartial, ErrorCode: "emby_refresh_failed"})

	videos, total, err := service.List(Filter{Kind: KindVideo})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, videos, 1)
	assert.Equal(t, video.TraceID(), videos[0].TraceID)
	assert.Equal(t, KindVideo, videos[0].Kind)

	actors, total, err := service.List(Filter{Kind: KindActor})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, actors, 1)
	assert.Equal(t, actor.TraceID(), actors[0].TraceID)
	assert.NotEqual(t, video.TraceID(), actor.TraceID())

	detail, err := service.Get(video.TraceID())
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	assert.Equal(t, uint64(1), detail.Events[0].Sequence)
	assert.Equal(t, "JavBus", detail.Run.SelectedProvider)
	assert.Equal(t, 1, detail.Run.ResultCount)
	require.NotNil(t, detail.Run.CompletedAt)

	partial, err := service.Get(actor.TraceID())
	require.NoError(t, err)
	assert.Equal(t, StatusPartial, partial.Run.Status)
	assert.Equal(t, "emby_refresh_failed", partial.Run.ErrorCode)
}

func TestConcurrentEventsKeepDenseOrdering(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "concurrent"})
	require.True(t, ok)

	const workers, perWorker = 8, 25
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for attempt := 0; attempt < perWorker; attempt++ {
				handle.Event(Event{
					Component: ComponentProvider,
					Stage:     StageProviderCompleted,
					Provider:  fmt.Sprintf("Provider-%d", worker),
					Attempt:   attempt,
				})
			}
		}(worker)
	}
	wait.Wait()

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	require.Len(t, detail.Events, workers*perWorker)
	for index, event := range detail.Events {
		assert.Equal(t, uint64(index+1), event.Sequence, "sequences must be dense and monotonic")
	}
	assert.Equal(t, workers*perWorker, detail.Run.EventCount)
}

func TestClientEventIngestIsIdempotent(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationEnrich, Query: "SSIS-002"})
	require.True(t, ok)

	event := Event{
		Component:      ComponentWindmill,
		Stage:          StageWindmillStep,
		Message:        "translation completed",
		IdempotencyKey: "job-1:translate",
	}
	stored, inserted, err := service.Append(handle.TraceID(), event)
	require.NoError(t, err)
	assert.True(t, inserted)
	assert.Equal(t, uint64(1), stored.Sequence)

	_, inserted, err = service.Append(handle.TraceID(), event)
	require.NoError(t, err)
	assert.False(t, inserted, "duplicate idempotency key must not be stored twice")

	_, inserted, err = service.Append(handle.TraceID(), Event{
		Component:      ComponentWindmill,
		Stage:          StageWindmillStep,
		Message:        "second step",
		IdempotencyKey: "job-1:artwork",
	})
	require.NoError(t, err)
	assert.True(t, inserted)

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	require.Len(t, detail.Events, 2)
	assert.Equal(t, uint64(2), detail.Events[1].Sequence)

	_, _, err = service.Append(NewID(), Event{})
	assert.ErrorIs(t, err, ErrTraceNotFound)
	_, _, err = service.Append("bad id!", Event{})
	assert.ErrorIs(t, err, ErrInvalidTraceID)
}

func TestEventCapPerRun(t *testing.T) {
	service := newTestService(t, func(cfg *Config) { cfg.MaxEventsPerRun = 3 })
	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "cap"})
	require.True(t, ok)

	for index := 0; index < 5; index++ {
		handle.Event(Event{Component: ComponentProvider, Stage: StageProviderStarted, Attempt: index})
	}

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	assert.Len(t, detail.Events, 3)
	assert.GreaterOrEqual(t, service.Counters().EventsCapped, uint64(2))
}

func TestRetentionNeverPrunesRunningTraces(t *testing.T) {
	service := newTestService(t, nil)
	old := time.Now().AddDate(0, 0, -90)

	running, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "running"})
	require.True(t, ok)
	finished, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "finished"})
	require.True(t, ok)
	finished.Finish(FinishInput{Status: StatusSucceeded})

	require.NoError(t, service.store.UpdateRun(running.TraceID(), map[string]any{"started_at": old}))
	require.NoError(t, service.store.UpdateRun(finished.TraceID(), map[string]any{"started_at": old, "completed_at": old}))

	pruned, err := service.store.PruneExpired(30)
	require.NoError(t, err)
	assert.EqualValues(t, 1, pruned)

	_, err = service.Get(running.TraceID())
	require.NoError(t, err, "a running trace must never be pruned")

	_, err = service.Get(finished.TraceID())
	assert.ErrorIs(t, err, ErrTraceNotFound)
}

func TestAbandonedTracesAreClosedThenReclaimed(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{Kind: KindActor, Operation: OperationEnrich, Query: "abandoned"})
	require.True(t, ok)
	require.NoError(t, service.store.UpdateRun(handle.TraceID(), map[string]any{
		"started_at": time.Now().Add(-24 * time.Hour),
	}))

	pruned, err := service.PruneOnce()
	require.NoError(t, err)
	assert.EqualValues(t, 1, pruned)

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, detail.Run.Status)
	assert.Equal(t, "abandoned", detail.Run.ErrorCode)
	assert.Equal(t, "trace was never finished", detail.Run.ErrorMessage)
}

func TestMaxRunsCapDropsOldestTraces(t *testing.T) {
	service := newTestService(t, func(cfg *Config) { cfg.MaxRuns = 3 })
	for index := 0; index < 6; index++ {
		handle, ok := service.Start(StartInput{
			Kind:      KindVideo,
			Operation: OperationLookup,
			Query:     fmt.Sprintf("cap-%d", index),
		})
		require.True(t, ok)
		handle.Finish(FinishInput{Status: StatusSucceeded})
	}

	_, err := service.PruneOnce()
	require.NoError(t, err)

	_, total, err := service.List(Filter{})
	require.NoError(t, err)
	assert.LessOrEqual(t, total, int64(3))
}

func TestSecretsNeverReachStoredTraces(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{
		Kind:       KindVideo,
		Operation:  OperationLookup,
		Query:      "SSIS-001",
		ClientName: "emby-plugin",
	})
	require.True(t, ok)
	handle.Event(Event{
		Component: ComponentFlareSolverr,
		Stage:     StageProviderFailed,
		Level:     LevelError,
		Message:   "upstream 403 with Bearer abcdef1234567890 and token=topsecretvalue",
		Details: JSONMap{
			"authorization": "Bearer abcdef1234567890",
			"cookie":        "session=abcdef123456",
			"api_key":       "abcdef123456",
			"provider":      "JavLibrary",
			"url":           "https://host/x?api_key=abcdef123456&ok=1",
		},
	})
	handle.Finish(FinishInput{Status: StatusFailed, ErrorMessage: "failed with token=topsecretvalue"})

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	blob, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"abcdef1234567890", "topsecretvalue", "session=abcdef123456", "abcdef123456"} {
		assert.NotContains(t, string(blob), secret, "secret leaked into trace data: %s", secret)
	}
	assert.Contains(t, string(blob), "JavLibrary")
	assert.Contains(t, string(blob), "SSIS-001")
}

func TestStoreFailureNeverBreaksLookups(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "broken store"})
	require.True(t, ok)

	sqlDB, err := service.store.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	// None of the following may panic or propagate a fatal error.
	handle.Event(Event{Component: ComponentProvider, Stage: StageProviderStarted})
	_, finished := handle.Finish(FinishInput{Status: StatusSucceeded})
	assert.False(t, finished)

	_, startOK := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup})
	assert.False(t, startOK)

	_, _, appendErr := service.Append(handle.TraceID(), Event{Stage: StageWindmillStep})
	assert.Error(t, appendErr)

	assert.Greater(t, service.Counters().StoreFailures, uint64(0))
}

func TestDisabledServiceIsSafeNoOp(t *testing.T) {
	service := NewService(Config{Enabled: false, DSN: filepath.Join(t.TempDir(), "traces.db")})
	defer func() { _ = service.Close() }()

	assert.False(t, service.Enabled())
	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup})
	assert.False(t, ok)
	assert.Nil(t, handle)

	handle.Event(Event{Component: ComponentMetaTube, Stage: StageCompleted})
	_, finished := handle.Finish(FinishInput{Status: StatusSucceeded})
	assert.False(t, finished)
	handle.SetSelected("JavBus", "SSIS-001")

	_, _, err := service.Append(NewID(), Event{})
	assert.ErrorIs(t, err, ErrDisabled)
	_, err = service.Get(NewID())
	assert.ErrorIs(t, err, ErrDisabled)
	_, _, err = service.List(Filter{})
	assert.ErrorIs(t, err, ErrDisabled)
	_, err = service.Delete(NewID())
	assert.ErrorIs(t, err, ErrDisabled)
	_, err = service.PurgeExpired()
	assert.ErrorIs(t, err, ErrDisabled)
	assert.Equal(t, Counters{}, service.Counters())
}

func TestContextHelpersRecordIntoActiveTrace(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{Kind: KindActor, Operation: OperationEnrich, Query: "Saeki Yumika"})
	require.True(t, ok)

	ctx := WithContext(context.Background(), handle)
	require.Equal(t, handle.TraceID(), TraceIDFromContext(ctx))

	Emit(ctx, Event{Component: ComponentWindmill, Stage: StageWindmillStarted, Message: "flow started"})
	TimedEmit(ctx, time.Now().Add(-25*time.Millisecond), Event{Component: ComponentEmby, Stage: StageEmbyWrite, Message: "write"})
	Select(ctx, "Minnano-AV", "12345")
	Changes(ctx, "Minnano-AV", []FieldChange{
		{Field: "name", Action: ActionUpdated, Provider: "Minnano-AV", ValueBytes: 12},
		{Field: "aliases", Action: ActionAdded, Provider: "Minnano-AV", ValueBytes: 30},
	})

	// Emitting against an untraced context must be a harmless no-op.
	Emit(context.Background(), Event{Component: ComponentMetaTube, Stage: StageCompleted})
	assert.Empty(t, FromContext(context.Background()))
	assert.Nil(t, FromContext(nil))
	assert.Empty(t, FromContext(nil).TraceID())

	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	require.Len(t, detail.Events, 3, "Select updates the summary rather than adding an event")
	assert.Equal(t, "Minnano-AV", detail.Run.SelectedProvider)
	assert.Equal(t, "12345", detail.Run.SelectedProviderID)
	assert.Greater(t, detail.Events[1].DurationMS, 0.0)

	changes, ok := detail.Events[2].Details["changes"]
	require.True(t, ok)
	blob, err := json.Marshal(changes)
	require.NoError(t, err)
	assert.Contains(t, string(blob), "value_bytes")
	assert.Contains(t, string(blob), "aliases")
}

func TestDeleteTraceRemovesSummaryAndEvents(t *testing.T) {
	service := newTestService(t, nil)
	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "delete me"})
	require.True(t, ok)
	handle.Event(Event{Component: ComponentProvider, Stage: StageProviderStarted})
	handle.Finish(FinishInput{Status: StatusSucceeded})

	deleted, err := service.Delete(handle.TraceID())
	require.NoError(t, err)
	assert.True(t, deleted)

	_, err = service.Get(handle.TraceID())
	assert.ErrorIs(t, err, ErrTraceNotFound)

	deleted, err = service.Delete(handle.TraceID())
	require.NoError(t, err)
	assert.False(t, deleted)

	events, err := service.store.GetEvents(handle.TraceID())
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestFiltersAndPagination(t *testing.T) {
	service := newTestService(t, nil)
	for index := 0; index < 5; index++ {
		handle, ok := service.Start(StartInput{
			Kind:       KindVideo,
			Operation:  OperationLookup,
			Query:      fmt.Sprintf("SSIS-%03d", index),
			ClientName: "jav-master-app",
			ClientIP:   "192.168.10.170",
		})
		require.True(t, ok)
		if index%2 == 0 {
			handle.Event(Event{
				Component: ComponentFlareSolverr,
				Stage:     StageProviderFailed,
				Level:     LevelError,
				Provider:  "JavLibrary",
				Message:   "challenge failed",
			})
		}
		handle.Finish(FinishInput{Status: StatusSucceeded, SelectedProvider: "JavBus"})
	}

	runs, total, err := service.List(Filter{Limit: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.Len(t, runs, 2)

	runs, total, err = service.List(Filter{Limit: 2, Offset: 4})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.Len(t, runs, 1)

	_, total, err = service.List(Filter{Text: "SSIS-003"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// Pasting a trace ID into the text filter must find that trace.
	_, total, err = service.List(Filter{Text: runs[0].TraceID})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	_, total, err = service.List(Filter{Client: "192.168.10.170"})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)

	_, total, err = service.List(Filter{Component: ComponentFlareSolverr})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)

	_, total, err = service.List(Filter{ErrorOnly: true})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)

	_, total, err = service.List(Filter{Provider: "JavBus", Status: StatusSucceeded})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)

	_, total, err = service.List(Filter{Kind: KindActor})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	since := time.Now().Add(-time.Hour)
	_, total, err = service.List(Filter{Since: &since})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)

	future := time.Now().Add(time.Hour)
	_, total, err = service.List(Filter{Since: &future})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}
