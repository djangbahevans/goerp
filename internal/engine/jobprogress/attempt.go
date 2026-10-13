// Package jobprogress buffers module job reports and stores browser-visible snapshots.
package jobprogress

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"sync"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/rs/zerolog/log"
)

const MaxResultBytes = 256 << 10

type Report struct {
	Percent     int
	Message     string
	Result      jsontext.Value
	HasProgress bool
}

// Recorder is scoped to one queued attempt. Finish persists results only on success.
type Recorder interface {
	ReportProgress(int, string)
	ReportResult([]byte) *abi.HostError
	Finish(bool)
}

type Attempt struct {
	mu       sync.Mutex
	report   Report
	reported bool
	dirty    bool
	stop     chan struct{}
	done     chan struct{}
	save     func(context.Context, Report) error
}

func NewAttempt(save func(context.Context, Report) error) *Attempt {
	a := &Attempt{stop: make(chan struct{}), done: make(chan struct{}), save: save}
	go a.run()
	return a
}

func (a *Attempt) ReportProgress(percent int, message string) {
	text := []rune(message)
	text = text[:min(len(text), 500)]
	a.mu.Lock()
	defer a.mu.Unlock()
	a.report.Percent, a.report.Message = min(max(percent, 0), 100), string(text)
	a.report.HasProgress, a.reported, a.dirty = true, true, true
}

func ValidateResult(value []byte) *abi.HostError {
	if len(value) > MaxResultBytes {
		return &abi.HostError{Code: abi.ErrCodeJobsResultTooLarge, Message: "job result exceeds 256 KiB"}
	}
	if !jsontext.Value(value).IsValid() {
		return &abi.HostError{Code: abi.ErrCodeJobsInvalidResult, Message: "job result must be a valid JSON document"}
	}
	return nil
}

func (a *Attempt) ReportResult(value []byte) *abi.HostError {
	if err := ValidateResult(value); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.report.Result = bytes.Clone(value)
	a.reported = true
	return nil
}

func (a *Attempt) run() {
	defer close(a.done)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-timer.C:
			a.flush(false)
			timer.Reset(time.Second)
		}
	}
}

func (a *Attempt) flush(final bool) {
	a.mu.Lock()
	if (!final && !a.dirty) || !a.reported {
		a.mu.Unlock()
		return
	}
	report := a.report
	if !final {
		report.Result = nil
	}
	a.dirty = false
	a.mu.Unlock()

	// Handler cancellation must not discard its final progress report.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.save(ctx, report); err != nil {
		log.Error().Err(err).Msg("store job progress")
		a.mu.Lock()
		a.dirty = true
		a.mu.Unlock()
	}
}

func (a *Attempt) Finish(success bool) {
	close(a.stop)
	<-a.done

	if !success {
		a.mu.Lock()
		a.report.Result = nil
		a.mu.Unlock()
	}

	a.flush(true)
}
