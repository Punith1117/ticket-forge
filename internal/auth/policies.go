package auth

import (
	"time"

	"github.com/Punith1117/ticket-forge/internal/ratelimit"
)

var (
	// PolicyLoginIP limits overall login attempts per client IP.
	// Capacity: 30, Refill: 1 token / 2 seconds.
	PolicyLoginIP = ratelimit.Policy{
		ID:             "auth:login:ip",
		Capacity:       30,
		RefillTokens:   1,
		RefillInterval: 2 * time.Second,
	}

	// PolicyLoginEmailFailure limits failed authentication attempts per normalized email.
	// Capacity: 10, Refill: 1 token / 60 seconds.
	PolicyLoginEmailFailure = ratelimit.Policy{
		ID:             "auth:login:email_failure",
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: 60 * time.Second,
	}

	// PolicyRegisterIP limits registration requests per client IP.
	// Capacity: 10, Refill: 1 token / 30 seconds.
	PolicyRegisterIP = ratelimit.Policy{
		ID:             "auth:register:ip",
		Capacity:       10,
		RefillTokens:   1,
		RefillInterval: 30 * time.Second,
	}

	// PolicyRegisterEmail limits registration requests per normalized email.
	// Capacity: 3, Refill: 1 token / 10 minutes.
	PolicyRegisterEmail = ratelimit.Policy{
		ID:             "auth:register:email",
		Capacity:       3,
		RefillTokens:   1,
		RefillInterval: 10 * time.Minute,
	}

	// PolicyRegisterCompleteIP limits registration completion attempts per client IP.
	// Capacity: 20, Refill: 1 token / 10 seconds.
	PolicyRegisterCompleteIP = ratelimit.Policy{
		ID:             "auth:register:complete:ip",
		Capacity:       20,
		RefillTokens:   1,
		RefillInterval: 10 * time.Second,
	}

	// PolicyRegisterCompleteToken limits registration completion attempts per registration token.
	// Capacity: 5, Refill: 1 token / 2 minutes.
	PolicyRegisterCompleteToken = ratelimit.Policy{
		ID:             "auth:register:complete:token",
		Capacity:       5,
		RefillTokens:   1,
		RefillInterval: 2 * time.Minute,
	}
)
