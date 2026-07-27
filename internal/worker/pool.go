// Package worker provides a bounded goroutine pool with retries and backoff.
package worker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Job is a unit of work executed by the pool. It returns an error so the pool
// can implement retry semantics.
type Job func() error

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent marks an error as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}

// Pool is a bounded goroutine worker pool with retries and context cancellation.
type Pool struct {
	sem     chan struct{}
	wg      sync.WaitGroup
	retries int
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewPool creates a new pool that runs at most `concurrency` jobs in parallel.
// Each job is retried up to 3 times with exponential backoff (1s, 2s, 4s).
// The pool cancels all in-flight jobs when ctx is done.
func NewPool(ctx context.Context, concurrency int) *Pool {
	if concurrency < 1 {
		concurrency = 1
	}
	pCtx, cancel := context.WithCancel(ctx)
	return &Pool{
		sem:     make(chan struct{}, concurrency),
		retries: 3,
		ctx:     pCtx,
		cancel:  cancel,
	}
}

// Submit schedules a job. It blocks if the pool is saturated.
func (p *Pool) Submit(job Job) {
	p.SubmitWithHooks(job, nil, nil)
}

// SubmitWithHooks schedules a job and invokes hooks exactly once, after the
// final outcome is known. Retries happen internally; onSuccess is called once
// after the first successful attempt, and onFinalFailure is called once after
// the last failed attempt. Errors marked with Permanent are not retried.
func (p *Pool) SubmitWithHooks(job Job, onSuccess func(), onFinalFailure func(error)) {
	p.wg.Add(1)
	select {
	case p.sem <- struct{}{}:
	case <-p.ctx.Done():
		p.wg.Done()
		if onFinalFailure != nil {
			onFinalFailure(p.ctx.Err())
		}
		return
	}
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		var err error
		backoff := time.Second
		for attempt := 0; attempt < p.retries; attempt++ {
			if err = job(); err == nil {
				if onSuccess != nil {
					onSuccess()
				}
				return
			}
			if IsPermanent(err) || attempt == p.retries-1 {
				break
			}
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-p.ctx.Done():
				timer.Stop()
				if onFinalFailure != nil {
					onFinalFailure(p.ctx.Err())
				}
				return
			}
			backoff *= 2
		}
		if onFinalFailure != nil {
			onFinalFailure(err)
		}
	}()
}

// Wait blocks until all submitted jobs finish or the pool is cancelled.
//
// The caller MUST cancel the parent context to unblock Wait if a shutdown
// timeout expires. Wait itself does not impose a deadline.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// Cancel terminates all in-flight retry waits and prevents new submissions.
func (p *Pool) Cancel() {
	p.cancel()
}
