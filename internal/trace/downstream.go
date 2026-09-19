package trace

// Downstream report states persisted on the trace summary.
const (
	// DownstreamNone means nothing has been reported yet.
	DownstreamNone = ""
	// DownstreamUnavailable means the trace is a server-only lookup, so no
	// downstream report is expected.
	DownstreamUnavailable = "unavailable"
	// DownstreamWindmill means Windmill reported but Emby has not.
	DownstreamWindmill = "windmill"
	// DownstreamEmby means Emby reported but Windmill has not.
	DownstreamEmby = "emby"
	// DownstreamComplete means both downstream systems reported.
	DownstreamComplete = "complete"
	// DownstreamFailed means a downstream stage reported an error.
	DownstreamFailed = "failed"
)

// DownstreamState describes what is known about Windmill/Emby reporting for one
// trace. It is derived from the persisted summary field, never from a scan of
// the event collection.
type DownstreamState struct {
	Status   string `json:"status"`
	Awaiting bool   `json:"awaiting_report"`
	Detail   string `json:"detail,omitempty"`
}

// DownstreamFor derives the downstream state of a trace.
//
// A server-only lookup or test never waits for a downstream report. A completed
// Windmill/Emby trace is never reported as still awaiting. Only traces whose own
// work succeeded (or partially succeeded) and whose downstream reporting is
// incomplete are marked as awaiting.
func DownstreamFor(run Run) DownstreamState {
	status := run.DownstreamStatus
	if !ValidDownstream(status) {
		status = DownstreamNone
	}
	if status == DownstreamNone && (run.Operation == OperationLookup || run.Operation == OperationTest) {
		return DownstreamState{
			Status: DownstreamUnavailable,
			Detail: "server-only lookup; no downstream stages are expected",
		}
	}

	state := DownstreamState{Status: status}
	switch status {
	case DownstreamComplete:
		state.Detail = "Windmill and Emby stages were reported"
	case DownstreamWindmill:
		state.Detail = "Windmill reported; Emby has not reported yet"
	case DownstreamEmby:
		state.Detail = "Emby reported; Windmill has not reported yet"
	case DownstreamFailed:
		state.Detail = "a downstream stage reported a failure"
	default:
		state.Detail = "no Windmill or Emby stage has been reported"
	}

	// Only a trace that finished its own work with success or partial success can
	// be waiting for somebody else.
	state.Awaiting = (run.Status == StatusSucceeded || run.Status == StatusPartial) &&
		status != DownstreamComplete && status != DownstreamFailed
	return state
}

// ValidDownstream reports whether a persisted downstream value is recognised.
func ValidDownstream(status string) bool {
	switch status {
	case DownstreamNone, DownstreamWindmill, DownstreamEmby, DownstreamComplete, DownstreamFailed:
		return true
	}
	return false
}

// downstreamSignal maps an event onto the downstream signal it carries. Events
// from Windmill or Emby report downstream progress; an error-level event from
// either one marks the downstream path as failed.
func downstreamSignal(component, level string) string {
	switch component {
	case ComponentWindmill:
		if level == LevelError {
			return DownstreamFailed
		}
		return DownstreamWindmill
	case ComponentEmby:
		if level == LevelError {
			return DownstreamFailed
		}
		return DownstreamEmby
	}
	return ""
}
