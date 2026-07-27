package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPermanentAndIsPermanent(t *testing.T) {
	if IsPermanent(nil) {
		t.Error("nil should not be permanent")
	}
	if IsPermanent(errors.New("plain")) {
		t.Error("plain error should not be permanent")
	}
	perm := Permanent(errors.New("fatal"))
	if !IsPermanent(perm) {
		t.Error("Permanent error should be detected")
	}
	if err := Permanent(nil); err != nil {
		t.Error("Permanent(nil) should return nil")
	}
	if IsPermanent(Permanent(errors.New("test"))) != true {
		t.Error("wrapped permanent error should be detected")
	}
}

func TestPoolJobSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := NewPool(ctx, 2)

	var successCalled, failureCalled int32
	pool.SubmitWithHooks(
		func() error { return nil },
		func() { atomic.AddInt32(&successCalled, 1) },
		func(error) { atomic.AddInt32(&failureCalled, 1) },
	)
	pool.Wait()

	if atomic.LoadInt32(&successCalled) != 1 {
		t.Errorf("success hook called %d times, want 1", successCalled)
	}
	if atomic.LoadInt32(&failureCalled) != 0 {
		t.Errorf("failure hook called %d times, want 0", failureCalled)
	}
}

func TestPoolJobRetriesAndFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := NewPool(ctx, 2)

	var attempts int32
	var failureCalled int32
	var finalErr error
	pool.SubmitWithHooks(
		func() error {
			atomic.AddInt32(&attempts, 1)
			return errors.New("fail")
		},
		func() {},
		func(err error) {
			atomic.AddInt32(&failureCalled, 1)
			finalErr = err
		},
	)
	pool.Wait()

	if atomic.LoadInt32(&attempts) != 3 { // default 3 retries
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if atomic.LoadInt32(&failureCalled) != 1 {
		t.Errorf("failure hook called %d times, want 1", failureCalled)
	}
	if finalErr == nil || finalErr.Error() != "fail" {
		t.Errorf("finalErr = %v, want 'fail'", finalErr)
	}
}

func TestPoolPermanentErrorSkipsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := NewPool(ctx, 2)

	var attempts int32
	pool.SubmitWithHooks(
		func() error {
			atomic.AddInt32(&attempts, 1)
			return Permanent(errors.New("permanent failure"))
		},
		nil,
		nil,
	)
	pool.Wait()

	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("permanent error should not retry: attempts = %d, want 1", attempts)
	}
}

func TestPoolMultipleJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := NewPool(ctx, 3)

	var completed int32
	for i := 0; i < 10; i++ {
		pool.SubmitWithHooks(
			func() error { return nil },
			func() { atomic.AddInt32(&completed, 1) },
			nil,
		)
	}
	pool.Wait()

	if atomic.LoadInt32(&completed) != 10 {
		t.Errorf("completed = %d, want 10", completed)
	}
}

func TestPoolConcurrencyLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	concurrency := 2
	pool := NewPool(ctx, concurrency)

	var maxConcurrent int32
	var current int32
	var jobCount int32

	for i := 0; i < 6; i++ {
		pool.SubmitWithHooks(
			func() error {
				n := atomic.AddInt32(&current, 1)
				for {
					prev := atomic.LoadInt32(&maxConcurrent)
					if n > prev {
						atomic.StoreInt32(&maxConcurrent, n)
						break
					}
					if n <= prev {
						break
					}
					// retry compare-and-update
					if n <= atomic.LoadInt32(&maxConcurrent) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				atomic.AddInt32(&current, -1)
				return nil
			},
			func() { atomic.AddInt32(&jobCount, 1) },
			nil,
		)
	}
	pool.Wait()

	if atomic.LoadInt32(&maxConcurrent) > int32(concurrency) {
		t.Errorf("max concurrent = %d, want <= %d", maxConcurrent, concurrency)
	}
}

func TestPoolContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := NewPool(ctx, 1)

	blocked := make(chan struct{})
	unblock := make(chan struct{})
	pool.SubmitWithHooks(
		func() error {
			close(blocked)
			<-unblock
			return nil
		},
		nil, nil,
	)

	<-blocked

	done := make(chan struct{})
	var cancelled int32
	go func() {
		pool.SubmitWithHooks(
			func() error { return nil },
			nil,
			func(err error) {
				if errors.Is(err, context.Canceled) {
					atomic.AddInt32(&cancelled, 1)
				}
				close(done)
			},
		)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	close(unblock)
	pool.Wait()

	select {
	case <-done:
	case <-time.After(time.Second):
	}

	if atomic.LoadInt32(&cancelled) != 1 {
		t.Errorf("cancelled = %d, want 1 (context cancellation should trigger onFinalFailure)", atomic.LoadInt32(&cancelled))
	}
}

func TestPoolNewPoolMinConcurrency(t *testing.T) {
	pool := NewPool(context.Background(), 0)
	if cap(pool.sem) != 1 {
		t.Errorf("concurrency 0 should be clamped to 1, got %d", cap(pool.sem))
	}

	pool = NewPool(context.Background(), -5)
	if cap(pool.sem) != 1 {
		t.Errorf("negative concurrency should be clamped to 1, got %d", cap(pool.sem))
	}
}

func TestPoolCancel(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 1)

	// Block the semaphore
	pool.SubmitWithHooks(
		func() error {
			time.Sleep(200 * time.Millisecond)
			return nil
		},
		nil, nil,
	)

	pool.Cancel()

	var failureCalled int32
	pool.SubmitWithHooks(
		func() error { return nil },
		nil,
		func(error) { atomic.AddInt32(&failureCalled, 1) },
	)
	pool.Wait()

	if atomic.LoadInt32(&failureCalled) != 1 {
		t.Errorf("Cancel should prevent new submissions: failureCalled = %d, want 1", failureCalled)
	}
}

func TestPoolRetryBackoffIncreases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := NewPool(ctx, 1)

	var timestamps []time.Time
	var mu spinMutex
	pool.SubmitWithHooks(
		func() error {
			mu.Lock()
			timestamps = append(timestamps, time.Now())
			mu.Unlock()
			return errors.New("retry me")
		},
		nil,
		func(error) {},
	)
	pool.Wait()

	if len(timestamps) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(timestamps))
	}
	d1 := timestamps[1].Sub(timestamps[0])
	d2 := timestamps[2].Sub(timestamps[1])
	if d1 < time.Second/2 || d2 < d1 {
		t.Errorf("backoff should increase: d1=%v, d2=%v", d1, d2)
	}
}

type spinMutex struct{ locked int32 }

func (m *spinMutex) Lock() {
	for !atomic.CompareAndSwapInt32(&m.locked, 0, 1) {
	}
}
func (m *spinMutex) Unlock() { atomic.StoreInt32(&m.locked, 0) }
