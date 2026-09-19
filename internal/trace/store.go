package trace

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/metatube-community/metatube-sdk-go/database"
)

const (
	// pruneBatchSize bounds how much work a single retention pass performs.
	pruneBatchSize = 500
)

// store is the bounded persistent trace store. Every method returns errors
// instead of panicking so that a trace problem can never fail a lookup.
type store struct {
	db  *gorm.DB
	cfg Config
}

func openStore(cfg Config) (*store, error) {
	if cfg.DSN == "" {
		return nil, errors.New("trace store requires a DSN")
	}
	db, err := database.Open(&database.Config{
		DSN:                  cfg.DSN,
		DisableAutomaticPing: true,
		// Trace bookkeeping must not flood the admin log viewer with its own
		// SQL, so the trace store stays silent and only reports through counters.
		LogLevel: logger.Silent,
		// A single writer connection keeps SQLite free of lock contention. Trace
		// volume is low, so serializing is an acceptable trade for reliability.
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&Run{}, &Event{}); err != nil {
		return nil, err
	}
	return &store{db: db, cfg: cfg}, nil
}

func (s *store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// CreateRun inserts a trace summary. Re-posting an existing trace ID is a no-op
// so that client retries stay idempotent.
func (s *store) CreateRun(run Run) (bool, error) {
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	run.UpdatedAt = time.Now()
	result := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&run)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// UpdateRun applies a partial update to a trace summary.
func (s *store) UpdateRun(traceID string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now()
	return s.db.Model(&Run{}).Where("trace_id = ?", traceID).Updates(updates).Error
}

// GetRun loads a trace summary.
func (s *store) GetRun(traceID string) (*Run, error) {
	run := &Run{}
	if err := s.db.Where("trace_id = ?", traceID).First(run).Error; err != nil {
		return nil, err
	}
	return run, nil
}

// GetEvents loads the ordered events of a trace.
func (s *store) GetEvents(traceID string) ([]Event, error) {
	events := make([]Event, 0)
	if err := s.db.Where("trace_id = ?", traceID).
		Order("sequence ASC").Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

// NextSequence returns the next event sequence for a trace, resuming correctly
// after a restart because it reads the persisted high-water mark.
func (s *store) NextSequence(traceID string) (uint64, error) {
	var maxSequence uint64
	if err := s.db.Model(&Event{}).
		Where("trace_id = ?", traceID).
		Select("COALESCE(MAX(sequence), 0)").
		Scan(&maxSequence).Error; err != nil {
		return 0, err
	}
	return maxSequence + 1, nil
}

// HasIdempotencyKey reports whether an event with the same key was stored.
func (s *store) HasIdempotencyKey(traceID, key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	var count int64
	if err := s.db.Model(&Event{}).
		Where("trace_id = ? AND idempotency_key = ?", traceID, key).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// AppendEvent stores one event. It never overwrites an existing sequence.
func (s *store) AppendEvent(event Event) (bool, error) {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if event.Level == "" {
		event.Level = LevelInfo
	}
	result := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ListRuns returns matching trace summaries plus the unpaged total.
func (s *store) ListRuns(filter Filter) ([]Run, int64, error) {
	query := s.db.Model(&Run{})
	if filter.TraceID != "" {
		query = query.Where("trace_id = ?", filter.TraceID)
	}
	if filter.RunID != "" {
		query = query.Where("run_id = ?", filter.RunID)
	}
	if filter.ParentTraceID != "" {
		query = query.Where("parent_trace_id = ?", filter.ParentTraceID)
	}
	if filter.Kind != "" {
		query = query.Where("kind = ?", filter.Kind)
	}
	if filter.Operation != "" {
		query = query.Where("operation = ?", filter.Operation)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Client != "" {
		query = query.Where("client_name = ? OR client_ip = ?", filter.Client, filter.Client)
	}
	if filter.Provider != "" {
		query = query.Where("selected_provider = ?", filter.Provider)
	}
	if filter.EmbyItemID != "" {
		query = query.Where("emby_item_id = ?", filter.EmbyItemID)
	}
	if filter.WindmillJobID != "" {
		query = query.Where("windmill_job_id = ?", filter.WindmillJobID)
	}
	if filter.ErrorOnly {
		query = query.Where("error_count > 0 OR status = ?", StatusFailed)
	}
	if filter.Text != "" {
		like := "%" + strings.ToLower(filter.Text) + "%"
		query = query.Where(
			"(LOWER(trace_id) LIKE ? OR LOWER(query) LIKE ? OR LOWER(normalized_query) LIKE ? OR LOWER(selected_provider_id) LIKE ? OR LOWER(error_message) LIKE ?)",
			like, like, like, like, like)
	}
	// Component matching inspects the structured events rather than free-form logs.
	if filter.Component != "" {
		query = query.Where(
			"EXISTS (SELECT 1 FROM trace_events e WHERE e.trace_id = trace_runs.trace_id AND e.component = ?)",
			filter.Component)
	}
	if filter.Since != nil {
		query = query.Where("started_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		query = query.Where("started_at <= ?", *filter.Until)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultFilterLimit
	}
	if limit > MaxFilterLimit {
		limit = MaxFilterLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	runs := make([]Run, 0)
	if err := query.Order("started_at DESC").Limit(limit).Offset(offset).Find(&runs).Error; err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}

// DeleteRun removes one trace and its events.
func (s *store) DeleteRun(traceID string) (bool, error) {
	deleted := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&Run{}).Where("trace_id = ?", traceID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		if err := tx.Where("trace_id = ?", traceID).Delete(&Event{}).Error; err != nil {
			return err
		}
		if err := tx.Where("trace_id = ?", traceID).Delete(&Run{}).Error; err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("delete trace %q: %w", traceID, err)
	}
	return deleted, nil
}

// CountRuns returns the number of stored trace summaries.
func (s *store) CountRuns() (int64, error) {
	var count int64
	err := s.db.Model(&Run{}).Count(&count).Error
	return count, err
}

// findRuns returns bounded runs matching a column, ordered oldest first.
func (s *store) findRuns(column, value string, limit int) ([]Run, error) {
	runs := make([]Run, 0, limit)
	if err := s.db.Model(&Run{}).
		Where(column+" = ?", value).
		Order("started_at ASC").
		Limit(limit).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// GroupRuns resolves the traces belonging to one run using only explicit
// relationships, in the documented priority order: run_id, windmill_job_id, then
// a parent/child trace relationship. It never groups traces by similarity.
func (s *store) GroupRuns(id string, limit int) (runs []Run, groupedBy string, truncated bool, err error) {
	if runs, err = s.findRuns("run_id", id, limit); err != nil {
		return nil, "", false, err
	}
	if len(runs) > 0 {
		return runs, GroupedByRun, len(runs) >= limit, nil
	}

	if runs, err = s.findRuns("windmill_job_id", id, limit); err != nil {
		return nil, "", false, err
	}
	if len(runs) > 0 {
		return runs, GroupedByWindmill, len(runs) >= limit, nil
	}

	// Single trace, optionally with explicitly related children.
	trace := &Run{}
	if err = s.db.Where("trace_id = ?", id).First(trace).Error; err != nil {
		return nil, "", false, err
	}
	runs = []Run{*trace}

	children, err := s.findRuns("parent_trace_id", id, limit-1)
	if err != nil {
		return nil, "", false, err
	}
	if len(children) > 0 {
		runs = append(runs, children...)
		return runs, GroupedByParent, len(runs) >= limit, nil
	}
	return runs, GroupedByTrace, false, nil
}

// ListEventsForTraces returns the ordered events of several traces in a single
// bounded query, so a grouped run does not need one query per trace.
func (s *store) ListEventsForTraces(traceIDs []string, limit int) ([]Event, bool, error) {
	if len(traceIDs) == 0 {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = MaxRunEvents
	}
	events := make([]Event, 0, limit)
	if err := s.db.Where("trace_id IN ?", traceIDs).
		Order("trace_id ASC, sequence ASC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, false, err
	}
	return events, len(events) >= limit, nil
}

// ListEventsBetween returns events of one trace inside a time window, used by the
// step-scoped log correlation.
func (s *store) ListEventsBetween(traceID string, since, until time.Time, limit int) ([]Event, error) {
	events := make([]Event, 0, limit)
	if err := s.db.Where("trace_id = ? AND at >= ? AND at <= ?", traceID, since, until).
		Order("sequence ASC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

// PruneExpired deletes completed traces older than the retention window. Running
// traces are never removed.
func (s *store) PruneExpired(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	total := int64(0)
	for {
		ids, err := s.expiredTraceIDs(cutoff, pruneBatchSize)
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		for _, id := range ids {
			if _, err := s.DeleteRun(id); err != nil {
				return total, err
			}
			total++
		}
		if len(ids) < pruneBatchSize {
			return total, nil
		}
	}
}

func (s *store) expiredTraceIDs(cutoff time.Time, limit int) ([]string, error) {
	runs := make([]Run, 0, limit)
	if err := s.db.Model(&Run{}).
		Where("status IN ?", []string{StatusSucceeded, StatusPartial, StatusFailed, StatusCancelled}).
		Where("COALESCE(completed_at, started_at) < ?", cutoff).
		Order("started_at ASC").
		Limit(limit).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.TraceID)
	}
	return ids, nil
}

// StaleRuns returns the IDs of traces that are still marked queued/running but
// started before the cutoff. They are closed by the service so retention can
// eventually reclaim them.
func (s *store) StaleRuns(cutoff time.Time, limit int) ([]string, error) {
	runs := make([]Run, 0, limit)
	if err := s.db.Model(&Run{}).
		Where("status IN ?", []string{StatusQueued, StatusRunning}).
		Where("started_at < ?", cutoff).
		Order("started_at ASC").
		Limit(limit).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.TraceID)
	}
	return ids, nil
}

// EnforceMaxRuns caps the number of stored traces, dropping the oldest completed
// traces first. Running traces are never removed.
func (s *store) EnforceMaxRuns(maxRuns int) (int64, error) {
	if maxRuns <= 0 {
		return 0, nil
	}
	total := int64(0)
	for {
		runs := make([]Run, 0, pruneBatchSize)
		if err := s.db.Model(&Run{}).
			Order("started_at DESC").
			Offset(maxRuns).
			Limit(pruneBatchSize).
			Find(&runs).Error; err != nil {
			return total, err
		}
		deleted := int64(0)
		for _, run := range runs {
			if !TerminalStatus(run.Status) {
				continue
			}
			if _, err := s.DeleteRun(run.TraceID); err != nil {
				return total, err
			}
			deleted++
		}
		total += deleted
		if deleted == 0 || len(runs) < pruneBatchSize {
			return total, nil
		}
	}
}
