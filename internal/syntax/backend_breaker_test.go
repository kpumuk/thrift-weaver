package syntax

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	parserbackend "github.com/kpumuk/thrift-weaver/internal/syntax/backend"
	ts "github.com/kpumuk/thrift-weaver/internal/syntax/treesitter"
)

type countingFailFactory struct {
	err   error
	mu    sync.Mutex
	calls int
}

func (f *countingFailFactory) Name() string {
	return "counting-fail-factory"
}

func (f *countingFailFactory) NewParser() (parserbackend.Parser, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return nil, f.err
}

func (f *countingFailFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type blockingFactory struct{}

func (f *blockingFactory) Name() string {
	return "blocking-factory"
}

func (f *blockingFactory) NewParser() (parserbackend.Parser, error) {
	return &blockingParser{}, nil
}

type blockingParser struct{}

func (p *blockingParser) Parse(ctx context.Context, _ []byte, _ *ts.Tree) (*ts.Tree, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingParser) Close() {}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestBackendBreakerOpensAfterRepeatedOOMFailures(t *testing.T) {
	restoreBreaker := ResetBackendBreakerForTesting()
	defer restoreBreaker()

	factory := &countingFailFactory{err: errors.New("out of memory")}
	restoreFactory := setParserFactoryForTesting(factory)
	defer restoreFactory()

	src := []byte("struct S { 1: string value }\n")
	for range breakerFailureThreshold {
		tree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///breaker-oom.thrift"})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !hasDiagnosticCode(tree.Diagnostics, DiagnosticInternalParse) {
			t.Fatalf("expected INTERNAL_PARSE diagnostic, got %+v", tree.Diagnostics)
		}
	}

	snapshot := BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerOpen) {
		t.Fatalf("breaker state=%q, want %q", snapshot.State, backendBreakerOpen)
	}
	if got := factory.callCount(); got != breakerFailureThreshold {
		t.Fatalf("factory call count=%d, want %d", got, breakerFailureThreshold)
	}

	tree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///breaker-short-circuit.thrift"})
	if err != nil {
		t.Fatalf("Parse short-circuit: %v", err)
	}
	if !hasDiagnosticCode(tree.Diagnostics, DiagnosticInternalParse) {
		t.Fatalf("expected INTERNAL_PARSE diagnostic after breaker open, got %+v", tree.Diagnostics)
	}
	if got := factory.callCount(); got != breakerFailureThreshold {
		t.Fatalf("factory should not be called after breaker opens, got %d", got)
	}
}

func TestBackendBreakerTreatsABIMismatchAsHardFailure(t *testing.T) {
	restoreBreaker := ResetBackendBreakerForTesting()
	defer restoreBreaker()

	restoreFactory := setParserFactoryForTesting(&failingFactory{err: ts.ErrWASMABIMismatch})
	defer restoreFactory()

	tree, err := Parse(context.Background(), []byte("struct S { 1: string value }\n"), ParseOptions{URI: "file:///abi.thrift"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !hasDiagnosticCode(tree.Diagnostics, DiagnosticInternalParse) {
		t.Fatalf("expected INTERNAL_PARSE diagnostic, got %+v", tree.Diagnostics)
	}

	snapshot := BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerClosed) {
		t.Fatalf("breaker state=%q, want closed after one failure", snapshot.State)
	}
	if snapshot.FailureCount != 1 {
		t.Fatalf("failure count=%d, want 1", snapshot.FailureCount)
	}
}

func TestBackendBreakerIgnoresContextDeadlineFailures(t *testing.T) {
	restoreBreaker := ResetBackendBreakerForTesting()
	defer restoreBreaker()

	restoreFactory := setParserFactoryForTesting(&blockingFactory{})
	defer restoreFactory()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err := Parse(ctx, []byte("struct S { 1: string value }\n"), ParseOptions{URI: "file:///deadline.thrift"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Parse error=%v, want deadline exceeded", err)
	}

	snapshot := BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerClosed) {
		t.Fatalf("breaker state=%q, want closed", snapshot.State)
	}
	if snapshot.FailureCount != 0 {
		t.Fatalf("failure count=%d, want 0", snapshot.FailureCount)
	}
}

func TestBackendBreakerHalfOpenRecoveryClosesAfterThreeSuccessfulProbes(t *testing.T) {
	restoreBreaker := ResetBackendBreakerForTesting()
	defer restoreBreaker()

	clock := newFakeClock(time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC))
	restoreClock := SetBackendBreakerClockForTesting(clock.Now)
	defer restoreClock()

	failFactory := &countingFailFactory{err: errors.New("backend unavailable")}
	restoreFactory := setParserFactoryForTesting(failFactory)
	defer restoreFactory()

	src := []byte("struct S { 1: string value }\n")
	for range breakerFailureThreshold {
		if _, err := Parse(context.Background(), src, ParseOptions{URI: "file:///probe-open.thrift"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
	}

	snapshot := BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerOpen) {
		t.Fatalf("breaker state=%q, want open", snapshot.State)
	}
	if snapshot.ProbeDelay != breakerInitialProbeDelay {
		t.Fatalf("probe delay=%s, want %s", snapshot.ProbeDelay, breakerInitialProbeDelay)
	}

	successFactory := &observingFactory{}
	restoreFactory = setParserFactoryForTesting(successFactory)
	defer restoreFactory()

	if _, err := Parse(context.Background(), src, ParseOptions{URI: "file:///probe-blocked.thrift"}); err != nil {
		t.Fatalf("Parse blocked: %v", err)
	}
	if got := successFactory.calls(); got != 0 {
		t.Fatalf("probe should not run before nextProbeAt, got %d calls", got)
	}

	clock.Advance(breakerInitialProbeDelay)
	tree, err := Parse(context.Background(), src, ParseOptions{URI: "file:///probe-1.thrift"})
	if err != nil {
		t.Fatalf("probe 1 parse: %v", err)
	}
	if tree.Root == NoNode {
		t.Fatalf("probe 1 returned degraded tree")
	}
	snapshot = BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerHalfOpen) || snapshot.ProbeSuccess != 1 || snapshot.ProbeDelay != time.Minute {
		t.Fatalf("unexpected probe 1 breaker snapshot: %+v", snapshot)
	}

	clock.Advance(time.Minute)
	if _, err := Parse(context.Background(), src, ParseOptions{URI: "file:///probe-2.thrift"}); err != nil {
		t.Fatalf("probe 2 parse: %v", err)
	}
	snapshot = BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerHalfOpen) || snapshot.ProbeSuccess != 2 || snapshot.ProbeDelay != 2*time.Minute {
		t.Fatalf("unexpected probe 2 breaker snapshot: %+v", snapshot)
	}

	clock.Advance(2 * time.Minute)
	if _, err := Parse(context.Background(), src, ParseOptions{URI: "file:///probe-3.thrift"}); err != nil {
		t.Fatalf("probe 3 parse: %v", err)
	}
	snapshot = BackendBreakerSnapshotForTesting()
	if snapshot.State != string(backendBreakerClosed) {
		t.Fatalf("breaker state=%q, want closed", snapshot.State)
	}
	if got := successFactory.calls(); got != 3 {
		t.Fatalf("success factory calls=%d, want 3", got)
	}
}
