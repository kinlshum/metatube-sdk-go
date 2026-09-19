package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustRun(t *testing.T, service *Service, traceID string) Run {
	t.Helper()
	detail, err := service.Get(traceID)
	require.NoError(t, err)
	return detail.Run
}

func TestDownstreamReportStates(t *testing.T) {
	service := newTestService(t, nil)

	// A server-only lookup expects no downstream report at all.
	serverOnly, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "SSIS-001"})
	require.True(t, ok)
	serverOnly.Finish(FinishInput{Status: StatusSucceeded})
	state := DownstreamFor(mustRun(t, service, serverOnly.TraceID()))
	assert.Equal(t, DownstreamUnavailable, state.Status)
	assert.False(t, state.Awaiting)

	// An identify with nothing reported yet is awaiting a client report.
	identify, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationIdentify, Query: "SSIS-002"})
	require.True(t, ok)
	identify.Finish(FinishInput{Status: StatusSucceeded})
	state = DownstreamFor(mustRun(t, service, identify.TraceID()))
	assert.Equal(t, DownstreamNone, state.Status)
	assert.True(t, state.Awaiting)

	// Windmill reported, Emby has not.
	windmill, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationIdentify, Query: "SSIS-003"})
	require.True(t, ok)
	_, _, err := service.Append(windmill.TraceID(), Event{
		Component: ComponentWindmill, Stage: StageWindmillStarted, Message: "job started",
	})
	require.NoError(t, err)
	windmill.Finish(FinishInput{Status: StatusSucceeded})
	state = DownstreamFor(mustRun(t, service, windmill.TraceID()))
	assert.Equal(t, DownstreamWindmill, state.Status)
	assert.True(t, state.Awaiting, "Emby has not reported yet")

	// Emby first, then Windmill: complete regardless of reporting order.
	complete, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationEnrich, Query: "SSIS-004"})
	require.True(t, ok)
	_, _, err = service.Append(complete.TraceID(), Event{
		Component: ComponentEmby, Stage: StageEmbyWrite, Message: "metadata written",
	})
	require.NoError(t, err)
	state = DownstreamFor(mustRun(t, service, complete.TraceID()))
	assert.Equal(t, DownstreamEmby, state.Status)
	assert.False(t, state.Awaiting, "a still-running trace is not waiting for a report")

	complete.Finish(FinishInput{Status: StatusPartial})
	state = DownstreamFor(mustRun(t, service, complete.TraceID()))
	assert.Equal(t, DownstreamEmby, state.Status)
	assert.True(t, state.Awaiting, "Windmill has not reported yet")

	_, _, err = service.Append(complete.TraceID(), Event{
		Component: ComponentWindmill, Stage: StageWindmillStep, Message: "translation done",
	})
	require.NoError(t, err)
	state = DownstreamFor(mustRun(t, service, complete.TraceID()))
	assert.Equal(t, DownstreamComplete, state.Status)
	assert.False(t, state.Awaiting, "a fully reported trace must not claim to be awaiting")

	// A downstream failure is surfaced and is not awaiting.
	failed, ok := service.Start(StartInput{Kind: KindActor, Operation: OperationEnrich, Query: "Saeki"})
	require.True(t, ok)
	_, _, err = service.Append(failed.TraceID(), Event{
		Component: ComponentEmby, Stage: StageEmbyWrite, Level: LevelError, Message: "write failed",
	})
	require.NoError(t, err)
	failed.Finish(FinishInput{Status: StatusPartial, ErrorCode: "emby_write_failed"})
	state = DownstreamFor(mustRun(t, service, failed.TraceID()))
	assert.Equal(t, DownstreamFailed, state.Status)
	assert.False(t, state.Awaiting)

	// A failed lookup never awaits downstream work.
	lookup, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationIdentify, Query: "SSIS-005"})
	require.True(t, ok)
	lookup.Finish(FinishInput{Status: StatusFailed, ErrorCode: "not_found"})
	state = DownstreamFor(mustRun(t, service, lookup.TraceID()))
	assert.False(t, state.Awaiting)
}

func TestResultCountKeepsExplicitZero(t *testing.T) {
	service := newTestService(t, nil)

	handle, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "SSIS-010"})
	require.True(t, ok)

	// Zero results is a real outcome and must be readable as zero, not unknown.
	handle.SetResult("JavBus", "SSIS-010", 0)
	run := mustRun(t, service, handle.TraceID())
	assert.Equal(t, 0, run.ResultCount)
	assert.Equal(t, "JavBus", run.SelectedProvider)
	assert.Equal(t, 0, handle.Run().ResultCount, "the live handle must agree with storage")

	// Finishing without a count keeps the recorded value.
	handle.Finish(FinishInput{Status: StatusSucceeded})
	run = mustRun(t, service, handle.TraceID())
	assert.Equal(t, 0, run.ResultCount)

	// An explicit count, including zero, overwrites a previous value.
	other, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "SSIS-011"})
	require.True(t, ok)
	other.SetResult("JavBus", "SSIS-011", 5)
	assert.Equal(t, 5, mustRun(t, service, other.TraceID()).ResultCount)

	zero := 0
	other.Finish(FinishInput{Status: StatusSucceeded, ResultCount: &zero})
	run = mustRun(t, service, other.TraceID())
	assert.Equal(t, 0, run.ResultCount, "an explicit zero must overwrite the earlier count")

	// Without any recorded selection the count is left alone.
	plain, ok := service.Start(StartInput{Kind: KindVideo, Operation: OperationLookup, Query: "SSIS-012"})
	require.True(t, ok)
	plain.Finish(FinishInput{Status: StatusSucceeded})
	assert.Equal(t, 0, mustRun(t, service, plain.TraceID()).ResultCount)
}
