package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// otpLimiters stores rate limiters per phone number.
// sync.Map is safe for concurrent use by multiple goroutines without additional locking.
var otpLimiters sync.Map

// GetOTPLimiter retrieves the rate limiter for a specific phone number,
// creating a new one if it does not already exist.
func GetOTPLimiter(phone string) *rate.Limiter {
	// rate.Every(10 * time.Minute / 3) allows 3 tokens per 10 minutes (1 token every 3m20s)
	// with a burst capability of 3 tokens.
	limiter, _ := otpLimiters.LoadOrStore(phone, rate.NewLimiter(rate.Every(10*time.Minute/3), 3))
	return limiter.(*rate.Limiter)
}

// AllowOTPRequest checks if a phone number is allowed to request an OTP code.
// It returns false if the rate limit is exceeded.
func AllowOTPRequest(phone string) bool {
	return GetOTPLimiter(phone).Allow()
}
