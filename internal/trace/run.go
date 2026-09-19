package trace

import "time"

// Bounds for one grouped run response.
const (
	// MaxRunTraces limits how many traces one grouped run may return.
	MaxRunTraces = 25
	// MaxRunEvents limits the events returned for a grouped run.
	MaxRunEvents = 500
	// MaxStepEvents limits the events returned for a step-scoped window.
	MaxStepEvents = 100
)

// Grouping keys, reported to the UI so it never has to guess why traces belong
// together.
const (
	GroupedByTrace    = "trace_id"
	GroupedByRun      = "run_id"
	GroupedByWindmill = "windmill_job_id"
	GroupedByParent   = "parent_trace_id"
)

// RunGroup is a bounded set of explicitly related traces plus their ordered
// events.
type RunGroup struct {
	RunID          string  `json:"run_id"`
	GroupedBy      string  `json:"grouped_by"`
	WindmillJobID  string  `json:"windmill_job_id,omitempty"`
	ParentTraceID  string  `json:"parent_trace_id,omitempty"`
	ChildCount     int     `json:"child_count"`
	Traces         []Run   `json:"traces"`
	Events         []Event `json:"events"`
	Truncated      bool    `json:"truncated"`
	EventTruncated bool    `json:"event_truncated"`
	TraceCount     int     `json:"trace_count"`
}

// ResolveRun returns the traces that explicitly belong to one run and their
// events in a single bounded query.
//
// Membership is only ever explicit, in the documented priority order: run_id,
// windmill_job_id, then a parent/child trace relationship. Traces are never
// grouped because a client name, catalog code, or timestamp looks similar.
func (s *Service) ResolveRun(runID string, eventLimit int) (*RunGroup, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	runID = NormalizeID(runID)
	if runID == "" {
		return nil, ErrInvalidTraceID
	}
	if eventLimit <= 0 || eventLimit > MaxRunEvents {
		eventLimit = MaxRunEvents
	}

	runs, groupedBy, truncated, err := s.store.GroupRuns(runID, MaxRunTraces)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return nil, ErrTraceNotFound
	}

	group := &RunGroup{
		RunID:      runID,
		GroupedBy:  groupedBy,
		Traces:     runs,
		TraceCount: len(runs),
		Truncated:  truncated,
	}
	traceIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		traceIDs = append(traceIDs, run.TraceID)
		if group.WindmillJobID == "" && run.WindmillJobID != "" {
			group.WindmillJobID = run.WindmillJobID
		}
		if groupedBy == GroupedByParent && run.ParentTraceID == "" {
			group.ParentTraceID = run.TraceID
		}
	}
	if groupedBy == GroupedByParent {
		group.ChildCount = len(runs) - 1
	}
	if group.ParentTraceID == "" && len(runs) > 0 {
		group.ParentTraceID = runs[0].ParentTraceID
	}

	events, eventTruncated, err := s.store.ListEventsForTraces(traceIDs, eventLimit)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return nil, err
	}
	group.Events = events
	group.EventTruncated = eventTruncated
	return group, nil
}

// StepWindow returns the bounded event window around one step, used to build the
// time range for step-scoped log correlation (event start minus two seconds
// through event end plus two seconds).
func StepWindow(event Event) (time.Time, time.Time) {
	start := event.At
	if start.IsZero() {
		now := time.Now()
		start = now
	}
	end := start.Add(time.Duration(event.DurationMS * float64(time.Millisecond)))
	return start.Add(-2 * time.Second), end.Add(2 * time.Second)
}
