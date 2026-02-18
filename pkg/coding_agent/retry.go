package coding_agent

import (
	"context"
	"math"
	"regexp"
	"sync"
	"time"
)

var retryablePattern = regexp.MustCompile(`(?i)(overloaded|rate.?limit|429|500|502|503|504|too many requests|capacity|temporarily unavailable|server error|internal error)`)

// RetryManager manages auto-retry logic with exponential backoff.
type RetryManager struct {
	mu          sync.Mutex
	attempt     int
	maxAttempts int
	baseDelayMs int
	maxDelayMs  int
	cancelFn    context.CancelFunc
	enabled     bool
}

// NewRetryManager creates a RetryManager with the given configuration.
func NewRetryManager(cfg RetryConfig, enabled bool) *RetryManager {
	maxAttempts := cfg.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	baseDelay := cfg.BaseDelayMs
	if baseDelay <= 0 {
		baseDelay = 1000
	}
	maxDelay := cfg.MaxDelayMs
	if maxDelay <= 0 {
		maxDelay = 30000
	}
	return &RetryManager{
		maxAttempts: maxAttempts,
		baseDelayMs: baseDelay,
		maxDelayMs:  maxDelay,
		enabled:     enabled,
	}
}

// IsRetryableError checks if an error message indicates a retryable error.
func IsRetryableError(errMsg string) bool {
	return retryablePattern.MatchString(errMsg)
}

// HandleRetryableError performs exponential backoff retry.
// It calls onRetryStart before waiting and retryFn to perform the retry.
// Returns nil if retry was initiated, or an error if retries are exhausted or aborted.
func (rm *RetryManager) HandleRetryableError(ctx context.Context, errMsg string, onRetryStart func(attempt, maxAttempts, delayMs int), retryFn func() error) error {
	rm.mu.Lock()
	if !rm.enabled {
		rm.mu.Unlock()
		return nil
	}
	rm.attempt++
	attempt := rm.attempt
	if attempt > rm.maxAttempts {
		rm.mu.Unlock()
		rm.Reset()
		return nil
	}

	// Calculate exponential backoff delay
	delay := float64(rm.baseDelayMs) * math.Pow(2, float64(attempt-1))
	if delay > float64(rm.maxDelayMs) {
		delay = float64(rm.maxDelayMs)
	}
	delayMs := int(delay)

	// Create cancellable context for this retry wait
	waitCtx, cancel := context.WithCancel(ctx)
	rm.cancelFn = cancel
	rm.mu.Unlock()

	if onRetryStart != nil {
		onRetryStart(attempt, rm.maxAttempts, delayMs)
	}

	// Wait with cancellation support
	timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-waitCtx.Done():
		cancel()
		return waitCtx.Err()
	case <-timer.C:
	}
	cancel()

	return retryFn()
}

// AbortRetry cancels any pending retry wait.
func (rm *RetryManager) AbortRetry() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.cancelFn != nil {
		rm.cancelFn()
		rm.cancelFn = nil
	}
}

// Reset resets the retry counter.
func (rm *RetryManager) Reset() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.attempt = 0
}

// IsEnabled returns whether auto-retry is enabled.
func (rm *RetryManager) IsEnabled() bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.enabled
}

// SetEnabled enables or disables auto-retry.
func (rm *RetryManager) SetEnabled(enabled bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.enabled = enabled
}
