package orchestrator

import "time"

func backoff(attempt int, cap time.Duration) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	d := 10 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= cap && cap >= 0 {
			return cap
		}
	}
	if cap >= 0 && d > cap {
		return cap
	}
	return d
}
