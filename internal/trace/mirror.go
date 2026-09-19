package trace

import "time"

// Source labels reported to a log mirror.
const (
	// MirrorSourceTrace marks a MetaTube trace record.
	MirrorSourceTrace = "trace"
)

// MirrorEvent is one transport-neutral record handed to a log mirror such as the
// Graylog GELF sender. It carries the required common fields (application,
// service, server, node, environment, logger and version are added by the
// transport) plus every correlation field the spec requires, so one run can be
// followed across MetaTube, the provider bridge, FlareSolverr, Windmill, the
// reverse proxy and the Emby report.
type MirrorEvent struct {
	At time.Time

	SourceType string

	TraceID       string
	RunID         string
	ParentTraceID string
	WindmillJobID string
	EmbyItemID    string
	Client        string

	Kind      string
	Operation string
	Status    string

	Component  string
	Stage      string
	Provider   string
	Attempt    int
	HTTPStatus int
	DurationMS float64

	Level   string
	Message string
	Details JSONMap
}

// Mirror receives structured trace records for durable, cross-service logging.
//
// Implementations must return quickly and must never block, panic or fail a
// metadata lookup: the trace service calls a mirror synchronously on the request
// path, so a mirror is expected to hand the record to a bounded asynchronous
// queue and drop (with accounting) when that queue is full.
type Mirror interface {
	Mirror(event MirrorEvent)
}

// SetMirror attaches an optional log mirror. Attaching a mirror never changes
// trace behaviour, and a mirror that fails stays invisible to lookups.
func (s *Service) SetMirror(mirror Mirror) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.mirror = mirror
	s.mu.Unlock()
}

// MirrorEnabled reports whether a log mirror is attached.
func (s *Service) MirrorEnabled() bool {
	return s != nil && s.mirrorOf() != nil
}

func (s *Service) mirrorOf() Mirror {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mirror
}

// mirrorRun returns the cached run-level correlation context for one trace,
// loading it from the store the first time an event is mirrored.
func (s *Service) mirrorRun(traceID string) Run {
	s.mu.Lock()
	if run, ok := s.mirrorRuns[traceID]; ok {
		s.mu.Unlock()
		return run
	}
	s.mu.Unlock()
	run, err := s.store.GetRun(traceID)
	if err != nil {
		return Run{TraceID: traceID}
	}
	s.mu.Lock()
	if s.mirrorRuns == nil {
		s.mirrorRuns = make(map[string]Run)
	}
	s.mirrorRuns[traceID] = *run
	s.trimCachesLocked()
	s.mu.Unlock()
	return *run
}

// cacheMirrorRun remembers a run for later mirroring.
func (s *Service) cacheMirrorRun(run Run) {
	if s == nil || run.TraceID == "" {
		return
	}
	s.mu.Lock()
	if s.mirrorRuns == nil {
		s.mirrorRuns = make(map[string]Run)
	}
	s.mirrorRuns[run.TraceID] = run
	s.mu.Unlock()
}

// mirrorEvent hands one record to the attached mirror. A mirror must never be
// able to break a lookup, so a panic is contained and reported here.
func (s *Service) mirrorEvent(record MirrorEvent) {
	mirror := s.mirrorOf()
	if mirror == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			traceLog.Printf("log mirror panicked: %v", recovered)
		}
	}()
	mirror.Mirror(record)
}

// mirrorRecord builds the mirror record for one event of a run.
func mirrorRecord(run Run, event Event) MirrorEvent {
	client := run.ClientName
	if client == "" {
		client = run.ClientIP
	}
	return MirrorEvent{
		At:            event.At,
		SourceType:    MirrorSourceTrace,
		TraceID:       run.TraceID,
		RunID:         run.RunID,
		ParentTraceID: run.ParentTraceID,
		WindmillJobID: run.WindmillJobID,
		EmbyItemID:    run.EmbyItemID,
		Client:        client,
		Kind:          run.Kind,
		Operation:     run.Operation,
		Status:        run.Status,
		Component:     event.Component,
		Stage:         event.Stage,
		Provider:      event.Provider,
		Attempt:       event.Attempt,
		HTTPStatus:    event.HTTPStatus,
		DurationMS:    event.DurationMS,
		Level:         event.Level,
		Message:       event.Message,
		Details:       event.Details,
	}
}

// mirrorStatusLevel maps a terminal trace status onto a log level.
func mirrorStatusLevel(status string) string {
	switch status {
	case StatusFailed:
		return LevelError
	case StatusCancelled:
		return LevelWarn
	}
	return LevelInfo
}
