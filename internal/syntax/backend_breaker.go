package syntax

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	breakerFailureThreshold      = 5
	breakerFailureWindow         = 60 * time.Second
	breakerInitialProbeDelay     = 30 * time.Second
	breakerMaxProbeDelay         = 10 * time.Minute
	breakerProbeSuccessThreshold = 3
)

var errParserBackendUnavailable = errors.New("parser backend unavailable")

type backendBreakerState string

const (
	backendBreakerClosed   backendBreakerState = "closed"
	backendBreakerOpen     backendBreakerState = "open"
	backendBreakerHalfOpen backendBreakerState = "half_open"
)

type backendAttempt struct {
	probe bool
}

// BackendBreakerSnapshot exposes breaker state for reliability tests.
type BackendBreakerSnapshot struct {
	State         string
	FailureCount  int
	ProbeDelay    time.Duration
	NextProbeAt   time.Time
	ProbeSuccess  int
	ProbeInFlight bool
}

type backendBreaker struct {
	mu            sync.Mutex
	now           func() time.Time
	state         backendBreakerState
	failures      []time.Time
	probeDelay    time.Duration
	nextProbeAt   time.Time
	probeSuccess  int
	probeInFlight bool
}

var runtimeBreaker = newBackendBreaker()

func newBackendBreaker() *backendBreaker {
	return &backendBreaker{
		now:   time.Now,
		state: backendBreakerClosed,
	}
}

func beginBackendAttempt() (backendAttempt, error) {
	return runtimeBreaker.begin()
}

func completeBackendAttemptSuccess(attempt backendAttempt) {
	runtimeBreaker.complete(attempt, nil)
}

func completeBackendAttemptFailure(attempt backendAttempt, err error) {
	runtimeBreaker.complete(attempt, err)
}

func isHardBackendFailure(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func (b *backendBreaker) begin() (backendAttempt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.pruneFailuresLocked(now)
	switch b.state {
	case backendBreakerClosed:
		return backendAttempt{}, nil
	case backendBreakerOpen, backendBreakerHalfOpen:
		if b.probeInFlight || now.Before(b.nextProbeAt) {
			return backendAttempt{}, errParserBackendUnavailable
		}
		b.state = backendBreakerHalfOpen
		b.probeInFlight = true
		return backendAttempt{probe: true}, nil
	default:
		return backendAttempt{}, nil
	}
}

func (b *backendBreaker) complete(attempt backendAttempt, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.pruneFailuresLocked(now)
	if !attempt.probe {
		b.completeClosedAttemptLocked(now, err)
		return
	}

	b.probeInFlight = false
	switch {
	case err == nil:
		b.probeSuccess++
		if b.probeSuccess >= breakerProbeSuccessThreshold {
			b.closeLocked()
			return
		}
		b.state = backendBreakerHalfOpen
		b.advanceProbeLocked(now)
	case isHardBackendFailure(err):
		b.probeSuccess = 0
		b.state = backendBreakerOpen
		b.advanceProbeLocked(now)
	default:
		b.state = backendBreakerHalfOpen
		b.advanceProbeLocked(now)
	}
}

func (b *backendBreaker) completeClosedAttemptLocked(now time.Time, err error) {
	if !isHardBackendFailure(err) {
		return
	}

	b.failures = append(b.failures, now)
	b.pruneFailuresLocked(now)
	if len(b.failures) < breakerFailureThreshold {
		return
	}

	b.state = backendBreakerOpen
	b.failures = nil
	b.probeSuccess = 0
	b.probeInFlight = false
	b.probeDelay = breakerInitialProbeDelay
	b.nextProbeAt = now.Add(b.probeDelay)
}

func (b *backendBreaker) advanceProbeLocked(now time.Time) {
	if b.probeDelay <= 0 {
		b.probeDelay = breakerInitialProbeDelay
	} else {
		b.probeDelay *= 2
		if b.probeDelay > breakerMaxProbeDelay {
			b.probeDelay = breakerMaxProbeDelay
		}
	}
	b.nextProbeAt = now.Add(b.probeDelay)
}

func (b *backendBreaker) closeLocked() {
	b.state = backendBreakerClosed
	b.failures = nil
	b.probeDelay = 0
	b.nextProbeAt = time.Time{}
	b.probeSuccess = 0
	b.probeInFlight = false
}

func (b *backendBreaker) pruneFailuresLocked(now time.Time) {
	if len(b.failures) == 0 {
		return
	}

	cutoff := now.Add(-breakerFailureWindow)
	keep := 0
	for _, failure := range b.failures {
		if failure.Before(cutoff) {
			continue
		}
		b.failures[keep] = failure
		keep++
	}
	b.failures = b.failures[:keep]
}

func (b *backendBreaker) snapshot() BackendBreakerSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.pruneFailuresLocked(now)
	return BackendBreakerSnapshot{
		State:         string(b.state),
		FailureCount:  len(b.failures),
		ProbeDelay:    b.probeDelay,
		NextProbeAt:   b.nextProbeAt,
		ProbeSuccess:  b.probeSuccess,
		ProbeInFlight: b.probeInFlight,
	}
}

func (b *backendBreaker) resetForTesting() func() {
	b.mu.Lock()
	prevState := b.state
	prevFailures := append([]time.Time(nil), b.failures...)
	prevProbeDelay := b.probeDelay
	prevNextProbeAt := b.nextProbeAt
	prevProbeSuccess := b.probeSuccess
	prevProbeInFlight := b.probeInFlight
	prevNow := b.now
	b.state = backendBreakerClosed
	b.failures = nil
	b.probeDelay = 0
	b.nextProbeAt = time.Time{}
	b.probeSuccess = 0
	b.probeInFlight = false
	b.now = time.Now
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		b.state = prevState
		b.failures = prevFailures
		b.probeDelay = prevProbeDelay
		b.nextProbeAt = prevNextProbeAt
		b.probeSuccess = prevProbeSuccess
		b.probeInFlight = prevProbeInFlight
		b.now = prevNow
		b.mu.Unlock()
	}
}

func (b *backendBreaker) setClockForTesting(now func() time.Time) func() {
	b.mu.Lock()
	prev := b.now
	b.now = now
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		b.now = prev
		b.mu.Unlock()
	}
}

// BackendBreakerSnapshotForTesting returns the current breaker state.
func BackendBreakerSnapshotForTesting() BackendBreakerSnapshot {
	return runtimeBreaker.snapshot()
}

// ResetBackendBreakerForTesting clears runtime breaker state for reliability tests.
func ResetBackendBreakerForTesting() func() {
	return runtimeBreaker.resetForTesting()
}

// SetBackendBreakerClockForTesting overrides the breaker clock for deterministic tests.
func SetBackendBreakerClockForTesting(now func() time.Time) func() {
	return runtimeBreaker.setClockForTesting(now)
}
