package retry

import "time"

// Delay returns the bounded delay before the next retry attempt.
func Delay(attempts int) time.Duration {
	if attempts < 1 { attempts = 1 }
	if attempts > 12 { attempts = 12 }
	return time.Duration(attempts*5) * time.Minute
}
