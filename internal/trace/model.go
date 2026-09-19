package trace

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Trace kinds. Video and actor traces are always kept separate.
const (
	KindVideo = "video"
	KindActor = "actor"
)

// Operations describe what the client asked MetaTube to do.
const (
	OperationLookup   = "lookup"
	OperationIdentify = "identify"
	OperationEnrich   = "enrich"
	OperationRefresh  = "refresh"
	OperationTest     = "test"
)

// Statuses describe the lifecycle of a trace.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusPartial   = "partial"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Components are the participating systems in a trace timeline.
const (
	ComponentClient       = "client"
	ComponentMetaTube     = "metatube"
	ComponentCache        = "cache"
	ComponentDatabase     = "database"
	ComponentThrottle     = "throttle"
	ComponentProvider     = "provider"
	ComponentFlareSolverr = "flaresolverr"
	ComponentTranslation  = "translation"
	ComponentWindmill     = "windmill"
	ComponentEmby         = "emby"
)

// Stages are the individual steps recorded inside a trace.
const (
	StageRequestReceived     = "request_received"
	StageNormalized          = "normalized"
	StageCacheLookup         = "cache_lookup"
	StageThrottleWait        = "throttle_wait"
	StageProviderStarted     = "provider_started"
	StageProviderCompleted   = "provider_completed"
	StageProviderFailed      = "provider_failed"
	StageFallbackStarted     = "fallback_started"
	StageResultSelected      = "result_selected"
	StageFieldChanged        = "field_changed"
	StageTranslationStarted  = "translation_started"
	StageTranslationComplete = "translation_completed"
	StageWindmillStarted     = "windmill_started"
	StageWindmillStep        = "windmill_step"
	StageEmbyWrite           = "emby_write"
	StageEmbyRefresh         = "emby_refresh"
	StageCompleted           = "completed"
	StageFailed              = "failed"
	StageCancelled           = "cancelled"
)

// Event levels.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// JSONMap stores a small, already-sanitized detail map as JSON text. It works on
// both SQLite and PostgreSQL, which the metadata database supports.
type JSONMap map[string]any

func (m JSONMap) Value() (driver.Value, error) {
	if len(m) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(map[string]any(m))
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (m *JSONMap) Scan(src any) error {
	if src == nil {
		*m = nil
		return nil
	}
	var data []byte
	switch typed := src.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return fmt.Errorf("unsupported detail type %T", src)
	}
	if len(data) == 0 {
		*m = nil
		return nil
	}
	value := map[string]any{}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*m = value
	return nil
}

// FieldChange records an enrichment field outcome without storing the value.
type FieldChange struct {
	Field      string `json:"field"`
	Action     string `json:"action"`
	Provider   string `json:"provider,omitempty"`
	ValueBytes int    `json:"value_bytes"`
}

// Field actions for enrichment summaries.
const (
	ActionAdded     = "added"
	ActionUpdated   = "updated"
	ActionUnchanged = "unchanged"
	ActionSkipped   = "skipped"
	ActionFailed    = "failed"
)

// Run is the durable summary of one trace.
type Run struct {
	TraceID            string     `gorm:"column:trace_id;size:64;primaryKey" json:"trace_id"`
	ParentTraceID      string     `gorm:"column:parent_trace_id;size:64;index" json:"parent_trace_id,omitempty"`
	Kind               string     `gorm:"column:kind;size:16;index" json:"kind"`
	Operation          string     `gorm:"column:operation;size:32;index" json:"operation"`
	Query              string     `gorm:"column:query;size:512" json:"query,omitempty"`
	NormalizedQuery    string     `gorm:"column:normalized_query;size:512" json:"normalized_query,omitempty"`
	ClientName         string     `gorm:"column:client_name;size:64;index" json:"client_name,omitempty"`
	ClientIP           string     `gorm:"column:client_ip;size:64;index" json:"client_ip,omitempty"`
	ClientPort         string     `gorm:"column:client_port;size:16" json:"client_port,omitempty"`
	UserAgent          string     `gorm:"column:user_agent;size:512" json:"user_agent,omitempty"`
	StartedAt          time.Time  `gorm:"column:started_at;index" json:"started_at"`
	CompletedAt        *time.Time `gorm:"column:completed_at" json:"completed_at,omitempty"`
	DurationMS         float64    `gorm:"column:duration_ms" json:"duration_ms"`
	Status             string     `gorm:"column:status;size:16;index" json:"status"`
	SelectedProvider   string     `gorm:"column:selected_provider;size:128;index" json:"selected_provider,omitempty"`
	SelectedProviderID string     `gorm:"column:selected_provider_id;size:256" json:"selected_provider_id,omitempty"`
	EmbyItemID         string     `gorm:"column:emby_item_id;size:128;index" json:"emby_item_id,omitempty"`
	WindmillJobID      string     `gorm:"column:windmill_job_id;size:128;index" json:"windmill_job_id,omitempty"`
	WindmillFlowPath   string     `gorm:"column:windmill_flow_path;size:512" json:"windmill_flow_path,omitempty"`
	ResultCount        int        `gorm:"column:result_count" json:"result_count"`
	WarningCount       int        `gorm:"column:warning_count" json:"warning_count"`
	ErrorCount         int        `gorm:"column:error_count" json:"error_count"`
	EventCount         int        `gorm:"column:event_count" json:"event_count"`
	DownstreamStatus   string     `gorm:"column:downstream_status;size:16;index" json:"downstream_status,omitempty"`
	ErrorCode          string     `gorm:"column:error_code;size:64" json:"error_code,omitempty"`
	ErrorMessage       string     `gorm:"column:error_message;size:512" json:"error_message,omitempty"`
	UpdatedAt          time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// TableName keeps trace data in dedicated, indexed tables.
func (Run) TableName() string { return "trace_runs" }

// Event is one structured step inside a trace.
type Event struct {
	TraceID        string    `gorm:"column:trace_id;size:64;primaryKey" json:"trace_id"`
	Sequence       uint64    `gorm:"column:sequence;primaryKey" json:"sequence"`
	At             time.Time `gorm:"column:at;index" json:"at"`
	DurationMS     float64   `gorm:"column:duration_ms" json:"duration_ms,omitempty"`
	Level          string    `gorm:"column:level;size:16" json:"level"`
	Component      string    `gorm:"column:component;size:32;index" json:"component"`
	Stage          string    `gorm:"column:stage;size:48;index" json:"stage"`
	Provider       string    `gorm:"column:provider;size:128;index" json:"provider,omitempty"`
	Attempt        int       `gorm:"column:attempt" json:"attempt,omitempty"`
	HTTPStatus     int       `gorm:"column:http_status" json:"http_status,omitempty"`
	Message        string    `gorm:"column:message;size:1024" json:"message,omitempty"`
	Details        JSONMap   `gorm:"column:details;type:text" json:"details,omitempty"`
	IdempotencyKey string    `gorm:"column:idempotency_key;size:128;index" json:"-"`
}

// TableName keeps events in their own indexed table.
func (Event) TableName() string { return "trace_events" }

// Filter selects traces for the admin list view.
type Filter struct {
	TraceID       string
	Kind          string
	Operation     string
	Status        string
	Client        string
	Provider      string
	Component     string
	Text          string
	EmbyItemID    string
	WindmillJobID string
	ErrorOnly     bool
	Since         *time.Time
	Until         *time.Time
	Limit         int
	Offset        int
}

// StartInput describes a new trace.
type StartInput struct {
	TraceID          string
	ParentTraceID    string
	Kind             string
	Operation        string
	Query            string
	NormalizedQuery  string
	ClientName       string
	ClientIP         string
	ClientPort       string
	UserAgent        string
	EmbyItemID       string
	WindmillJobID    string
	WindmillFlowPath string
	Status           string
}

// FinishInput completes a trace.
type FinishInput struct {
	Status             string
	SelectedProvider   string
	SelectedProviderID string
	EmbyItemID         string
	WindmillJobID      string
	WindmillFlowPath   string
	// ResultCount is a pointer so that an explicit zero (a lookup that genuinely
	// found nothing) is distinguishable from "the caller supplied no count".
	ResultCount  *int
	ErrorCode    string
	ErrorMessage string
	DurationMS   float64
}

// RunDetail is a summary plus its ordered events.
type RunDetail struct {
	Run
	Events []Event `json:"events"`
}

// Counters track trace bookkeeping. They exist so that storage problems are
// visible without ever affecting metadata lookups.
type Counters struct {
	Started        uint64 `json:"started"`
	Finished       uint64 `json:"finished"`
	EventsStored   uint64 `json:"events_stored"`
	EventsCapped   uint64 `json:"events_capped"`
	EventsRejected uint64 `json:"events_rejected"`
	StoreFailures  uint64 `json:"store_failures"`
	Dropped        uint64 `json:"dropped"`
	PrunedRuns     uint64 `json:"pruned_runs"`
}
