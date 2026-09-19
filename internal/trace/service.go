package trace

import (
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/metatube-community/metatube-sdk-go/internal/logbuffer"
)

// staleRunningTimeout closes traces that were started but never finished, so
// that abandoned runs cannot accumulate forever. Traces are never pruned while
// they are still marked running.
const staleRunningTimeout = 6 * time.Hour

var traceLog = log.New(logbuffer.Output(), "[TRACE]\u0020", log.LstdFlags)

// Service owns the trace store and all in-flight trace handles. Every method is
// safe for concurrent use, and no method ever returns an error that a metadata
// lookup would have to handle.
type Service struct {
	cfg   Config
	store *store

	mu    sync.Mutex
	runs  map[string]*RunHandle
	count map[string]uint64
	seq   map[string]uint64

	counters counters

	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

type counters struct {
	started        atomic.Uint64
	finished       atomic.Uint64
	eventsStored   atomic.Uint64
	eventsCapped   atomic.Uint64
	eventsRejected atomic.Uint64
	storeFailures  atomic.Uint64
	prunedRuns     atomic.Uint64
}

// Disabled returns a service that records nothing. It is the default for SDK
// consumers so that tracing stays opt-in and silent.
func Disabled() *Service {
	return &Service{
		cfg:   Config{}.WithDefaults(),
		runs:  make(map[string]*RunHandle),
		count: make(map[string]uint64),
		seq:   make(map[string]uint64),
		stop:  make(chan struct{}),
	}
}

// NewService opens the persistent store. When tracing is disabled or the store
// cannot be opened, the returned service is a safe no-op so that the server
// keeps working exactly as before.
func NewService(cfg Config) *Service {
	cfg = cfg.WithDefaults()
	service := &Service{
		cfg:   cfg,
		runs:  make(map[string]*RunHandle),
		count: make(map[string]uint64),
		seq:   make(map[string]uint64),
		stop:  make(chan struct{}),
	}
	if !cfg.Enabled {
		traceLog.Printf("disabled (METATUBE_TRACE_ENABLED=false)")
		return service
	}
	store, err := openStore(cfg)
	if err != nil {
		traceLog.Printf("store unavailable, tracing disabled: %v", err)
		return service
	}
	service.store = store
	traceLog.Printf("enabled: dsn=%s retention_days=%d max_runs=%d max_events_per_run=%d",
		cfg.DSN, cfg.RetentionDays, cfg.MaxRuns, cfg.MaxEventsPerRun)
	service.wg.Add(1)
	go service.pruneLoop()
	return service
}

// Enabled reports whether traces are being recorded.
func (s *Service) Enabled() bool { return s != nil && s.store != nil }

// Config returns the effective configuration.
func (s *Service) Config() Config {
	if s == nil {
		return Config{}.WithDefaults()
	}
	return s.cfg
}

// Counters returns a snapshot of trace bookkeeping.
func (s *Service) Counters() Counters {
	if s == nil {
		return Counters{}
	}
	return Counters{
		Started:        s.counters.started.Load(),
		Finished:       s.counters.finished.Load(),
		EventsStored:   s.counters.eventsStored.Load(),
		EventsCapped:   s.counters.eventsCapped.Load(),
		EventsRejected: s.counters.eventsRejected.Load(),
		StoreFailures:  s.counters.storeFailures.Load(),
		PrunedRuns:     s.counters.prunedRuns.Load(),
	}
}

// Close stops background pruning and closes the store.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
	if s.store == nil {
		return nil
	}
	return s.store.Close()
}

// Start opens a new trace. It returns false when tracing is unavailable or the
// input is invalid; callers must treat tracing as best-effort.
func (s *Service) Start(input StartInput) (*RunHandle, bool) {
	if !s.Enabled() {
		return nil, false
	}
	traceID := NormalizeID(input.TraceID)
	if traceID == "" {
		traceID = NewID()
	}
	kind := input.Kind
	if !ValidKind(kind) {
		kind = KindVideo
	}
	operation := input.Operation
	if !ValidOperation(operation) {
		operation = OperationLookup
	}
	status := input.Status
	if !ValidStatus(status) {
		status = StatusRunning
	}
	now := time.Now()
	normalized := input.NormalizedQuery
	if normalized == "" {
		normalized = input.Query
	}
	run := Run{
		TraceID:          traceID,
		ParentTraceID:    NormalizeID(input.ParentTraceID),
		Kind:             kind,
		Operation:        operation,
		Query:            SanitizeQuery(input.Query),
		NormalizedQuery:  SanitizeQuery(normalized),
		ClientName:       Truncate(SanitizeString(input.ClientName), 64),
		ClientIP:         Truncate(SanitizeString(input.ClientIP), 64),
		ClientPort:       Truncate(SanitizeString(input.ClientPort), 16),
		UserAgent:        Truncate(SanitizeString(input.UserAgent), 512),
		EmbyItemID:       Truncate(SanitizeString(input.EmbyItemID), 128),
		WindmillJobID:    Truncate(SanitizeString(input.WindmillJobID), 128),
		WindmillFlowPath: Truncate(SanitizeString(input.WindmillFlowPath), 512),
		Status:           status,
		StartedAt:        now,
	}
	created, err := s.store.CreateRun(run)
	if err != nil {
		s.counters.storeFailures.Add(1)
		traceLog.Printf("start %s failed: %v", traceID, err)
		return nil, false
	}
	s.counters.started.Add(1)
	handle := &RunHandle{service: s, run: run, created: created}
	s.mu.Lock()
	s.runs[traceID] = handle
	s.mu.Unlock()
	return handle, true
}

// Sentinel errors surfaced to the admin API. They never affect lookups.
var (
	ErrDisabled       = errors.New("tracing is disabled")
	ErrInvalidTraceID = errors.New("invalid trace id")
	ErrTraceNotFound  = errors.New("trace not found")
)

// maxCachedTraces bounds the per-trace bookkeeping caches.
const maxCachedTraces = 40000

// Append stores a client-reported event, honouring an idempotency key. A
// duplicate key is reported as (not stored, no error) so retries are safe.
func (s *Service) Append(traceID string, event Event) (Event, bool, error) {
	if !s.Enabled() {
		return Event{}, false, ErrDisabled
	}
	traceID = NormalizeID(traceID)
	if traceID == "" {
		return Event{}, false, ErrInvalidTraceID
	}
	if _, err := s.store.GetRun(traceID); err != nil {
		return Event{}, false, ErrTraceNotFound
	}
	if key := NormalizeID(event.IdempotencyKey); key != "" {
		exists, err := s.store.HasIdempotencyKey(traceID, key)
		if err != nil {
			s.counters.storeFailures.Add(1)
			return Event{}, false, err
		}
		if exists {
			return Event{}, false, nil
		}
		event.IdempotencyKey = key
	}
	stored, ok := s.appendEvent(traceID, event)
	return stored, ok, nil
}

func (s *Service) appendEvent(traceID string, event Event) (Event, bool) {
	if !s.Enabled() || traceID == "" {
		return Event{}, false
	}
	if !s.reserveEventSlot(traceID) {
		s.counters.eventsCapped.Add(1)
		return Event{}, false
	}
	sequence, err := s.reserveSequence(traceID)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return Event{}, false
	}

	event.TraceID = traceID
	event.Sequence = sequence
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if event.Level == "" {
		event.Level = LevelInfo
	}
	if event.Component == "" {
		event.Component = ComponentMetaTube
	}
	event.Component = Truncate(SanitizeString(event.Component), 32)
	event.Stage = Truncate(SanitizeString(event.Stage), 48)
	event.Provider = Truncate(SanitizeString(event.Provider), 128)
	event.Message = SanitizeString(event.Message)
	event.Details = SanitizeDetails(event.Details)
	event.IdempotencyKey = NormalizeID(event.IdempotencyKey)

	inserted, err := s.store.AppendEvent(event)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return Event{}, false
	}
	if !inserted {
		return Event{}, false
	}
	s.counters.eventsStored.Add(1)
	s.bumpRun(traceID, event.Level)
	return event, true
}

// reserveEventSlot enforces METATUBE_TRACE_MAX_EVENTS_PER_RUN.
func (s *Service) reserveEventSlot(traceID string) bool {
	limit := uint64(s.cfg.MaxEventsPerRun)
	s.mu.Lock()
	defer s.mu.Unlock()
	count, ok := s.count[traceID]
	if !ok {
		// Lazily resume the count after a restart.
		if run, err := s.store.GetRun(traceID); err == nil {
			count = uint64(run.EventCount)
		}
	}
	if count >= limit {
		return false
	}
	s.count[traceID] = count + 1
	s.trimCachesLocked()
	return true
}

// reserveSequence hands out the next per-trace event sequence, resuming from the
// persisted high-water mark when the trace is not in memory.
func (s *Service) reserveSequence(traceID string) (uint64, error) {
	s.mu.Lock()
	if sequence, ok := s.seq[traceID]; ok {
		s.seq[traceID] = sequence + 1
		s.mu.Unlock()
		return sequence, nil
	}
	s.mu.Unlock()

	next, err := s.store.NextSequence(traceID)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence, ok := s.seq[traceID]; ok {
		s.seq[traceID] = sequence + 1
		return sequence, nil
	}
	s.seq[traceID] = next + 1
	s.trimCachesLocked()
	return next, nil
}

func (s *Service) trimCachesLocked() {
	if len(s.count) > maxCachedTraces {
		s.count = make(map[string]uint64)
	}
	if len(s.seq) > maxCachedTraces {
		s.seq = make(map[string]uint64)
	}
}

// bumpRun keeps the summary counters in step with the stored events.
func (s *Service) bumpRun(traceID, level string) {
	updates := map[string]any{"event_count": gorm.Expr("event_count + 1")}
	switch level {
	case LevelError:
		updates["error_count"] = gorm.Expr("error_count + 1")
	case LevelWarn:
		updates["warning_count"] = gorm.Expr("warning_count + 1")
	}
	if err := s.store.UpdateRun(traceID, updates); err != nil {
		s.counters.storeFailures.Add(1)
	}
}

// Finish completes a trace. It is also the endpoint clients use to report their
// downstream stages (Windmill job, Emby write) against the same trace ID.
func (s *Service) Finish(traceID string, input FinishInput) (*Run, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	traceID = NormalizeID(traceID)
	if traceID == "" {
		return nil, ErrInvalidTraceID
	}
	run, err := s.store.GetRun(traceID)
	if err != nil {
		return nil, ErrTraceNotFound
	}

	status := input.Status
	if !ValidStatus(status) {
		status = StatusSucceeded
	}
	now := time.Now()
	duration := input.DurationMS
	if duration <= 0 && !run.StartedAt.IsZero() {
		duration = float64(now.Sub(run.StartedAt).Microseconds()) / 1000
	}

	updates := map[string]any{
		"status":      status,
		"duration_ms": duration,
	}
	if TerminalStatus(status) {
		updates["completed_at"] = now
	}
	setIfPresent := func(column, value string, limit int) {
		if sanitized := Truncate(SanitizeString(value), limit); sanitized != "" {
			updates[column] = sanitized
		}
	}
	setIfPresent("selected_provider", input.SelectedProvider, 128)
	setIfPresent("selected_provider_id", input.SelectedProviderID, 256)
	setIfPresent("emby_item_id", input.EmbyItemID, 128)
	setIfPresent("windmill_job_id", input.WindmillJobID, 128)
	setIfPresent("windmill_flow_path", input.WindmillFlowPath, 512)
	setIfPresent("error_code", input.ErrorCode, 64)
	setIfPresent("error_message", input.ErrorMessage, 512)
	if input.ResultCount > 0 {
		updates["result_count"] = input.ResultCount
	}

	if err := s.store.UpdateRun(traceID, updates); err != nil {
		s.counters.storeFailures.Add(1)
		return nil, err
	}
	s.counters.finished.Add(1)

	updated, err := s.store.GetRun(traceID)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Get returns a trace summary with its ordered events.
func (s *Service) Get(traceID string) (*RunDetail, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	traceID = NormalizeID(traceID)
	if traceID == "" {
		return nil, ErrInvalidTraceID
	}
	run, err := s.store.GetRun(traceID)
	if err != nil {
		return nil, ErrTraceNotFound
	}
	events, err := s.store.GetEvents(traceID)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return nil, err
	}
	return &RunDetail{Run: *run, Events: events}, nil
}

// List returns trace summaries matching a filter.
func (s *Service) List(filter Filter) ([]Run, int64, error) {
	if !s.Enabled() {
		return nil, 0, ErrDisabled
	}
	runs, total, err := s.store.ListRuns(filter)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return nil, 0, err
	}
	return runs, total, nil
}

// Delete removes a single trace and its events.
func (s *Service) Delete(traceID string) (bool, error) {
	if !s.Enabled() {
		return false, ErrDisabled
	}
	traceID = NormalizeID(traceID)
	if traceID == "" {
		return false, ErrInvalidTraceID
	}
	deleted, err := s.store.DeleteRun(traceID)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return false, err
	}
	s.mu.Lock()
	delete(s.runs, traceID)
	delete(s.count, traceID)
	delete(s.seq, traceID)
	s.mu.Unlock()
	return deleted, nil
}

// PurgeExpired enforces retention and the run cap. The admin API requires an
// explicit confirmation before calling it.
func (s *Service) PurgeExpired() (int64, error) { return s.PruneOnce() }

// PruneOnce closes abandoned traces, then applies retention and the run cap.
func (s *Service) PruneOnce() (int64, error) {
	if !s.Enabled() {
		return 0, ErrDisabled
	}
	stale, err := s.closeStale()
	if err != nil {
		s.counters.storeFailures.Add(1)
		return 0, err
	}
	expired, err := s.store.PruneExpired(s.cfg.RetentionDays)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return 0, err
	}
	excess, err := s.store.EnforceMaxRuns(s.cfg.MaxRuns)
	if err != nil {
		s.counters.storeFailures.Add(1)
		return 0, err
	}
	total := stale + expired + excess
	if total > 0 {
		s.counters.prunedRuns.Add(uint64(total))
		traceLog.Printf("pruned %d traces (abandoned=%d expired=%d over_cap=%d)", total, stale, expired, excess)
	}
	return total, nil
}

// closeStale marks abandoned running traces as failed so retention can reclaim
// them. Running traces are otherwise never pruned.
func (s *Service) closeStale() (int64, error) {
	cutoff := time.Now().Add(-staleRunningTimeout)
	total := int64(0)
	for {
		ids, err := s.store.StaleRuns(cutoff, pruneBatchSize)
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		for _, id := range ids {
			if err := s.store.UpdateRun(id, map[string]any{
				"status":        StatusFailed,
				"completed_at":  time.Now(),
				"error_code":    "abandoned",
				"error_message": "trace was never finished",
			}); err != nil {
				return total, err
			}
			total++
		}
		if len(ids) < pruneBatchSize {
			return total, nil
		}
	}
}

func (s *Service) pruneLoop() {
	defer s.wg.Done()
	if _, err := s.PruneOnce(); err != nil {
		traceLog.Printf("initial prune failed: %v", err)
	}
	ticker := time.NewTicker(s.cfg.PruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if _, err := s.PruneOnce(); err != nil {
				traceLog.Printf("prune failed: %v", err)
			}
		}
	}
}

type RunHandle struct {
	service *Service
	run     Run
	created bool

	mu                 sync.Mutex
	status             string
	selectedProvider   string
	selectedProviderID string
}

// TraceID returns the trace identifier.
func (h *RunHandle) TraceID() string {
	if h == nil {
		return ""
	}
	return h.run.TraceID
}

// Kind returns the trace kind (video or actor).
func (h *RunHandle) Kind() string {
	if h == nil {
		return ""
	}
	return h.run.Kind
}

// Operation returns the recorded operation.
func (h *RunHandle) Operation() string {
	if h == nil {
		return ""
	}
	return h.run.Operation
}

// Created reports whether this handle actually inserted the row, which lets the
// middleware tell "started here" apart from "continued from a client".
func (h *RunHandle) Created() bool {
	if h == nil {
		return false
	}
	return h.created
}

// Status returns the current status.
func (h *RunHandle) Status() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == "" {
		return h.run.Status
	}
	return h.status
}

// Run returns a snapshot of the trace summary.
func (h *RunHandle) Run() Run {
	if h == nil {
		return Run{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot := h.run
	if h.status != "" {
		snapshot.Status = h.status
	}
	if h.selectedProvider != "" {
		snapshot.SelectedProvider = h.selectedProvider
	}
	if h.selectedProviderID != "" {
		snapshot.SelectedProviderID = h.selectedProviderID
	}
	return snapshot
}

// Service returns the owning service.
func (h *RunHandle) Service() *Service {
	if h == nil {
		return nil
	}
	return h.service
}

// Event records one structured event against the trace.
func (h *RunHandle) Event(event Event) {
	if h == nil || h.service == nil {
		return
	}
	h.service.appendEvent(h.run.TraceID, event)
}

// EventError records a failed stage.
func (h *RunHandle) EventError(component, stage, provider, message string, details JSONMap) {
	h.Event(Event{
		Level:     LevelError,
		Component: component,
		Stage:     stage,
		Provider:  provider,
		Message:   message,
		Details:   details,
	})
}

// SetSelected records the provider result chosen for identify/enrichment.
func (h *RunHandle) SetSelected(provider, id string) {
	if h == nil || h.service == nil || !h.service.Enabled() || provider == "" {
		return
	}
	provider = Truncate(SanitizeString(provider), 128)
	id = Truncate(SanitizeString(id), 256)
	h.mu.Lock()
	h.selectedProvider, h.selectedProviderID = provider, id
	h.mu.Unlock()
	if err := h.service.store.UpdateRun(h.run.TraceID, map[string]any{
		"selected_provider":    provider,
		"selected_provider_id": id,
	}); err != nil {
		h.service.counters.storeFailures.Add(1)
	}
}

// FieldChanges records a redacted enrichment field-change summary as one event.
func (h *RunHandle) FieldChanges(provider string, changes []FieldChange) {
	if len(changes) == 0 {
		return
	}
	summary := make([]map[string]any, 0, len(changes))
	for _, change := range SanitizeFieldChanges(changes) {
		summary = append(summary, map[string]any{
			"field":       Truncate(SanitizeString(change.Field), 64),
			"action":      Truncate(SanitizeString(change.Action), 16),
			"provider":    Truncate(SanitizeString(change.Provider), 64),
			"value_bytes": change.ValueBytes,
		})
	}
	h.Event(Event{
		Component: ComponentMetaTube,
		Stage:     StageFieldChanged,
		Provider:  provider,
		Message:   "enrichment field changes reported",
		Details:   JSONMap{"changes": summary, "count": len(summary)},
	})
}

// Finish completes the trace. The returned summary reflects the stored row.
func (h *RunHandle) Finish(input FinishInput) (Run, bool) {
	if h == nil || h.service == nil {
		return Run{}, false
	}
	run, err := h.service.Finish(h.run.TraceID, input)
	if err != nil {
		return h.Run(), false
	}
	h.mu.Lock()
	h.status = run.Status
	if run.SelectedProvider != "" {
		h.selectedProvider = run.SelectedProvider
		h.selectedProviderID = run.SelectedProviderID
	}
	h.mu.Unlock()
	return *run, true
}
