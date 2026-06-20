package execution

import (
	"errors"
	"math"
	"time"

	"go.uber.org/zap"
)

// ResilientExecutor wraps API calls with resilience
type ResilientExecutor struct {
	logger              *zap.Logger
	maxRetries          int
	backoffBase         float64
	circuitBreakerOpen  bool
	consecutiveFailures int
	maxFailuresBeforeCB int
}

// NewResilientExecutor creates resilient executor
func NewResilientExecutor(logger *zap.Logger) *ResilientExecutor {
	return &ResilientExecutor{
		logger:              logger,
		maxRetries:          3,
		backoffBase:         0.5,
		maxFailuresBeforeCB: 10,
	}
}

// CallWithRetry executes an API call with automatic retry
func (re *ResilientExecutor) CallWithRetry(apiCall func() error) error {
	for attempt := 0; attempt < re.maxRetries; attempt++ {
		err := apiCall()
		if err == nil {
			re.consecutiveFailures = 0
			return nil
		}

		// Check error type
		if isRateLimited(err) {
			retryAfter := 2 * time.Second
			re.logger.Warn("Rate limited, backing off",
				zap.Duration("retry_after", retryAfter),
				zap.Int("attempt", attempt+1),
			)
			time.Sleep(retryAfter)
			continue
		}

		if isAuthError(err) {
			re.logger.Error("Auth token expired", zap.Error(err))
			re.circuitBreakerOpen = true
			return err
		}

		if isTransientError(err) {
			backoff := math.Pow(re.backoffBase, float64(attempt))
			duration := time.Duration(backoff*1000) * time.Millisecond
			re.logger.Warn("Transient error, backing off",
				zap.Duration("backoff", duration),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
			time.Sleep(duration)
			continue
		}

		// Permanent error
		re.logger.Error("Permanent API error", zap.Error(err))
		re.consecutiveFailures++

		if re.consecutiveFailures >= re.maxFailuresBeforeCB {
			re.logger.Critical("Too many failures, opening circuit breaker")
			re.circuitBreakerOpen = true
		}

		return err
	}

	re.logger.Error("Max retries exhausted")
	return errors.New("max retries exceeded")
}

// IsCircuitBreakerOpen checks if circuit breaker is open
func (re *ResilientExecutor) IsCircuitBreakerOpen() bool {
	return re.circuitBreakerOpen
}

// ResetCircuitBreaker resets circuit breaker
func (re *ResilientExecutor) ResetCircuitBreaker() {
	re.circuitBreakerOpen = false
	re.consecutiveFailures = 0
	re.logger.Info("Circuit breaker reset")
}

func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	// In real app, check HTTP 429
	return err.Error() == "rate limited" || err.Error() == "429"
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "unauthorized" || err.Error() == "401"
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "timeout" || msg == "500" || msg == "503" || msg == "502"
}

// HandleMarginChange detects and handles margin requirement changes
func (re *ResilientExecutor) HandleMarginChange(availableMargin float64) error {
	if availableMargin < 10000 {
		re.logger.Warn("Available margin critically low",
			zap.Float64("available", availableMargin),
		)
		// In production: reduce position sizes, tighten SLs
		return errors.New("low margin")
	}
	return nil
}

// HandleWebSocketDisconnect handles WebSocket disconnection
func (re *ResilientExecutor) HandleWebSocketDisconnect() error {
	re.logger.Critical("WebSocket disconnected, switching to polling mode")
	// In production: switch to HTTP polling, increase frequency
	return nil
}

// SimulateAPICall simulates an API call for testing
func (re *ResilientExecutor) SimulateAPICall(shouldFail bool) error {
	if shouldFail {
		return errors.New("simulated error")
	}
	return nil
}
