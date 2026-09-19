package trace

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMirror records every mirrored event and can be made to block or panic.
type fakeMirror struct {
	mu      sync.Mutex
	events  []MirrorEvent
	panics  bool
	blocked chan struct{}
}

func (m *fakeMirror) Mirror(event MirrorEvent) {
	if m.blocked != nil {
		<-m.blocked
	}
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()
	if m.panics {
		panic("mirror failure")
	}
}

func (m *fakeMirror) all() []MirrorEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MirrorEvent(nil), m.events...)
}

func (m *fakeMirror) find(stage string) (MirrorEvent, bool) {
	for _, event := range m.all() {
		if event.Stage == stage {
			return event, true
		}
	}
	return MirrorEvent{}, false
}

func TestMirrorReceivesRunCorrelation(t *testing.T) {
	service := newTestService(t, nil)
	mirror := &fakeMirror{}
	service.SetMirror(mirror)

	handle, ok := service.Start(StartInput{
		Kind:          KindVideo,
		Operation:     OperationIdentify,
		Query:         "SSIS-001",
		RunID:         "run-mirror-1111",
		WindmillJobID: "job-mirror-1111",
		ParentTraceID: "parent-mirror-1111",
		ClientName:    "emby-plugin",
		EmbyItemID:    "item-mirror-1111",
	})
	require.True(t, ok)

	handle.Event(Event{
		Component:  ComponentProvider,
		Stage:      StageProviderCompleted,
		Provider:   "JavBus",
		Attempt:    1,
		HTTPStatus: 200,
		DurationMS: 12.5,
		Message:    "provider responded",
		Details:    JSONMap{"result_count": 2},
	})
	handle.Finish(FinishInput{Status: StatusSucceeded, SelectedProvider: "JavBus"})

	events := mirror.all()
	require.GreaterOrEqual(t, len(events), 3)

	// The start record opens the run in the durable log.
	started, found := mirror.find(StageRequestReceived)
	require.True(t, found)
	assert.Equal(t, MirrorSourceTrace, started.SourceType)
	assert.Equal(t, "run-mirror-1111", started.RunID)
	assert.Equal(t, "job-mirror-1111", started.WindmillJobID)
	assert.Equal(t, "emby-plugin", started.Client)
	assert.Equal(t, "item-mirror-1111", started.EmbyItemID)

	// The step record carries the whole correlation set.
	step, found := mirror.find(StageProviderCompleted)
	require.True(t, found)
	assert.Equal(t, started.TraceID, step.TraceID)
	assert.Equal(t, "run-mirror-1111", step.RunID)
	assert.Equal(t, "parent-mirror-1111", step.ParentTraceID)
	assert.Equal(t, "job-mirror-1111", step.WindmillJobID)
	assert.Equal(t, KindVideo, step.Kind)
	assert.Equal(t, ComponentProvider, step.Component)
	assert.Equal(t, "JavBus", step.Provider)
	assert.EqualValues(t, 200, step.HTTPStatus)
	assert.Equal(t, LevelInfo, step.Level)

	// The finish record closes it with the stored outcome.
	finished, found := mirror.find(StageCompleted)
	require.True(t, found)
	assert.Equal(t, StatusSucceeded, finished.Status)
	assert.Equal(t, "JavBus", finished.Provider)
	assert.EqualValues(t, StatusSucceeded, finished.Details["status"])
}

func TestMirrorFailureNeverBreaksTracing(t *testing.T) {
	service := newTestService(t, nil)
	mirror := &fakeMirror{panics: true}
	service.SetMirror(mirror)

	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "SSIS-002"})
	require.True(t, ok)
	handle.Event(Event{Component: ComponentMetaTube, Stage: StageCacheLookup, Message: "cache miss"})
	handle.Finish(FinishInput{Status: StatusSucceeded})

	// The mirror panicked on every record, yet every step is still stored.
	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	assert.Len(t, detail.Events, 1)
	assert.Equal(t, StatusSucceeded, detail.Run.Status)
	assert.GreaterOrEqual(t, len(mirror.all()), 3)
}

func TestMirrorIsSilentWhenDisabled(t *testing.T) {
	service := newTestService(t, nil)
	assert.False(t, service.MirrorEnabled())

	var nilService *Service
	nilService.SetMirror(&fakeMirror{})
	assert.False(t, nilService.MirrorEnabled())

	// Events still record normally without a mirror.
	handle, ok := service.Start(StartInput{Kind: KindActor, Operation: OperationLookup, Query: "Saeki"})
	require.True(t, ok)
	handle.Event(Event{Component: ComponentMetaTube, Stage: StageCompleted})
	detail, err := service.Get(handle.TraceID())
	require.NoError(t, err)
	assert.Len(t, detail.Events, 1)
}

func TestClientReportedEventsAreMirroredWithRunContext(t *testing.T) {
	service := newTestService(t, nil)
	mirror := &fakeMirror{}
	service.SetMirror(mirror)

	handle, ok := service.Start(StartInput{
		Kind:          KindVideo,
		Operation:     OperationLookup,
		Query:         "SSIS-003",
		RunID:         "run-client-1111",
		WindmillJobID: "job-client-1111",
		ClientName:    "windmill",
	})
	require.True(t, ok)

	// A remote caller (Windmill, bridge) reports a step through the ingest API.
	stored, inserted, err := service.Append(handle.TraceID(), Event{
		Component:      ComponentWindmill,
		Stage:          StageWindmillStep,
		Message:        "windmill step reported",
		IdempotencyKey: "windmill-step-1",
	})
	require.NoError(t, err)
	require.True(t, inserted)

	event, found := mirror.find(StageWindmillStep)
	require.True(t, found)
	assert.Equal(t, stored.TraceID, event.TraceID)
	assert.Equal(t, "run-client-1111", event.RunID)
	assert.Equal(t, "job-client-1111", event.WindmillJobID)
	assert.Equal(t, "windmill", event.Client)
	assert.Equal(t, ComponentWindmill, event.Component)
	assert.WithinDuration(t, time.Now(), event.At, time.Minute)
}
