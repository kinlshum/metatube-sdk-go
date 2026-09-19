package gelf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/logbuffer"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// HeaderToken is the ingestion authentication header used by the Graylog GELF
// HTTP input. The token only ever travels in this header.
const HeaderToken = "X-Graylog-Token"

// SourceTypeHealth labels the records sent by Probe.
const SourceTypeHealth = "health"

var gelfLog = log.New(logbuffer.Output(), "[GELF]\u0020", log.LstdFlags)

// ErrNotConfigured reports that the sender has no usable endpoint or token.
var ErrNotConfigured = errors.New("graylog gelf sender is not configured")

// The sender must be usable as the trace service's log mirror.
var _ trace.Mirror = (*Sender)(nil)

// Sender delivers structured records to a Graylog GELF HTTP input through a
// bounded queue and one background worker. Every method is safe for concurrent
// use, and no method blocks a caller while Graylog is slow or unreachable.
type Sender struct {
	cfg    Config
	client *http.Client
	queue  chan trace.MirrorEvent
	quit   chan struct{}
	wg     sync.WaitGroup

	closeOnce sync.Once
	closed    atomic.Bool

	sent    atomic.Uint64
	failed  atomic.Uint64
	retried atomic.Uint64
	dropped atomic.Uint64

	lastDropLog atomic.Int64

	mu            sync.Mutex
	lastSuccessAt *time.Time
	lastFailureAt *time.Time
	lastError     string
	lastProbeAt   *time.Time
	probeOK       bool
	probeDetail   string
}

// New builds a sender. When the configuration is incomplete the returned sender
// is inert but still reports its configuration, so the admin UI can explain why
// nothing is being sent.
func New(cfg Config) *Sender {
	cfg = cfg.WithDefaults(hostname())
	sender := &Sender{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			// A redirect could send the ingestion token to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		quit: make(chan struct{}),
	}
	if !cfg.Configured() {
		if cfg.Enabled {
			gelfLog.Printf("not configured: %v", cfg.Validate())
		}
		return sender
	}
	sender.queue = make(chan trace.MirrorEvent, cfg.QueueSize)
	sender.wg.Add(1)
	go sender.worker()
	gelfLog.Printf("enabled: endpoint=%s server=%s node=%s environment=%s queue=%d timeout=%s retries=%d",
		cfg.Endpoint(), cfg.Server, cfg.Node, cfg.Environment, cfg.QueueSize, cfg.Timeout, cfg.MaxRetries)
	return sender
}

// NewFromEnv builds a sender from the METATUBE_GELF_* environment.
func NewFromEnv() *Sender { return New(ConfigFromEnv()) }

// Config returns the effective configuration, which never contains the token in
// its JSON form.
func (s *Sender) Config() Config {
	if s == nil {
		return Config{}.WithDefaults(hostname())
	}
	return s.cfg
}

// Enabled reports whether records are being queued for delivery.
func (s *Sender) Enabled() bool { return s != nil && s.queue != nil }

// Send queues one record. It never blocks: when the queue is full or the sender
// is closed the record is counted as dropped, so a lookup is never delayed.
func (s *Sender) Send(event trace.MirrorEvent) {
	if !s.Enabled() {
		return
	}
	if s.closed.Load() {
		s.dropped.Add(1)
		return
	}
	select {
	case s.queue <- event:
	default:
		s.dropped.Add(1)
		s.noteDrop()
	}
}

// Mirror implements trace.Mirror.
func (s *Sender) Mirror(event trace.MirrorEvent) { s.Send(event) }

func (s *Sender) worker() {
	defer s.wg.Done()
	for {
		select {
		case event := <-s.queue:
			s.deliver(event)
		case <-s.quit:
			s.drain()
			return
		}
	}
}

// drain makes one bounded, best-effort flush of the queued records at shutdown.
func (s *Sender) drain() {
	deadline := time.Now().Add(s.cfg.Timeout)
	for time.Now().Before(deadline) {
		select {
		case event := <-s.queue:
			payload, err := s.payload(event)
			if err != nil {
				s.failed.Add(1)
				s.noteFailure(err)
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
			_, err = s.post(ctx, payload)
			cancel()
			if err != nil {
				s.failed.Add(1)
				s.noteFailure(err)
				continue
			}
			s.sent.Add(1)
			s.noteSuccess()
		default:
			return
		}
	}
}

// deliver sends one record with bounded retries and backoff.
func (s *Sender) deliver(event trace.MirrorEvent) {
	payload, err := s.payload(event)
	if err != nil {
		s.failed.Add(1)
		s.noteFailure(err)
		return
	}
	attempts := s.cfg.MaxRetries + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			s.retried.Add(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
		retryable, err := s.post(ctx, payload)
		cancel()
		if err == nil {
			s.sent.Add(1)
			s.noteSuccess()
			return
		}
		if !retryable || attempt == attempts {
			s.failed.Add(1)
			s.noteFailure(err)
			return
		}
		if !s.sleep(s.cfg.RetryBackoff * time.Duration(attempt)) {
			s.dropped.Add(1)
			return
		}
	}
}

// post performs one delivery attempt and reports whether retrying can help. A
// client error (for example a rotated or wrong token) is never retried, so a
// misconfiguration cannot turn into a retry storm.
func (s *Sender) post(ctx context.Context, payload []byte) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint(), bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderToken, s.cfg.Token)
	response, err := s.client.Do(request)
	if err != nil {
		return true, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}()
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return false, nil
	case response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= 500:
		return true, fmt.Errorf("graylog rejected the record: HTTP %d", response.StatusCode)
	default:
		return false, fmt.Errorf("graylog rejected the record: HTTP %d", response.StatusCode)
	}
}

// sleep waits for the backoff, returning false when the sender is closing.
func (s *Sender) sleep(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.quit:
		return false
	}
}

// Probe sends one health record and waits for the result, so the admin can test
// ingestion reachability without waiting for real traffic. A probe never counts
// as a delivery.
func (s *Sender) Probe(ctx context.Context) error {
	now := time.Now().UTC()
	if !s.cfg.Enabled {
		err := fmt.Errorf("%w: %s=false", ErrNotConfigured, EnabledEnv)
		s.noteProbe(false, err)
		return err
	}
	if err := s.cfg.Validate(); err != nil {
		s.noteProbe(false, err)
		return fmt.Errorf("%w: %v", ErrNotConfigured, err)
	}
	payload, err := s.payload(trace.MirrorEvent{
		At:         now,
		SourceType: SourceTypeHealth,
		Level:      trace.LevelInfo,
		Component:  trace.ComponentMetaTube,
		Stage:      "health_probe",
		Message:    "metatube gelf probe",
		Details:    trace.JSONMap{"probe": true},
	})
	if err != nil {
		s.noteProbe(false, err)
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	if _, err := s.post(probeCtx, payload); err != nil {
		s.noteProbe(false, err)
		return err
	}
	s.noteProbe(true, nil)
	return nil
}

// Close stops the worker after a bounded flush. It is safe to call twice.
func (s *Sender) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.quit)
	})
	s.wg.Wait()
	return nil
}

func (s *Sender) noteSuccess() {
	now := time.Now().UTC()
	s.mu.Lock()
	s.lastSuccessAt = &now
	s.lastError = ""
	s.mu.Unlock()
}

func (s *Sender) noteFailure(err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	// The message is built from the endpoint and the status code only, and is
	// sanitized again so that an unexpected error cannot carry a secret out.
	message := trace.Truncate(trace.SanitizeString(err.Error()), 256)
	s.mu.Lock()
	s.lastFailureAt = &now
	s.lastError = message
	s.mu.Unlock()
	gelfLog.Printf("delivery failed: %s", message)
}

func (s *Sender) noteProbe(ok bool, err error) {
	now := time.Now().UTC()
	detail := "ingestion reachable"
	if !ok && err != nil {
		detail = trace.Truncate(trace.SanitizeString(err.Error()), 256)
	}
	s.mu.Lock()
	s.lastProbeAt = &now
	s.probeOK = ok
	s.probeDetail = detail
	s.mu.Unlock()
}

// noteDrop logs the first drop in each window, so a full queue is visible
// without flooding the log buffer.
func (s *Sender) noteDrop() {
	now := time.Now().Unix()
	last := s.lastDropLog.Load()
	if now-last < 30 {
		return
	}
	if !s.lastDropLog.CompareAndSwap(last, now) {
		return
	}
	gelfLog.Printf("queue full: dropped=%d queued=%d", s.dropped.Load(), len(s.queue))
}

func hostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}
