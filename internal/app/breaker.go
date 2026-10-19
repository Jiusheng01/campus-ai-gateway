package app

import (
	"sync"
	"time"
)

// The circuit breaker stops the gateway from hammering a channel that is
// already failing. It is per Provider, in process memory: it is health state,
// not durable truth, and it is deliberately per instance — a second instance
// keeps its own view until a shared store is introduced (out of scope).
//
// The thresholds are constants rather than configuration on purpose: a channel
// that fails five times in a row is down by any measure, and a knob an operator
// can get wrong is worse than a sensible default until someone needs otherwise.
const (
	// breakerFailureThreshold is how many consecutive failures open the circuit.
	breakerFailureThreshold = 5
	// breakerCooldown is how long the circuit stays open before one probe is let
	// through.
	breakerCooldown = 30 * time.Second
)

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

func (s breakerState) String() string {
	switch s {
	case breakerOpen:
		return "open"
	case breakerHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

type providerCircuit struct {
	state         breakerState
	failures      int
	openedAt      time.Time
	probeInFlight bool
}

// circuitBreaker holds every provider's circuit and the clock it decides on.
type circuitBreaker struct {
	mu  sync.Mutex
	m   map[string]*providerCircuit
	now func() time.Time
}

func newCircuitBreaker() *circuitBreaker {
	return &circuitBreaker{m: make(map[string]*providerCircuit), now: time.Now}
}

// withClock overrides the clock so a test can drive the cooldown and the
// half-open probe without sleeping (the same technique as
// Limiter.WithConcurrencyLease).
func (b *circuitBreaker) withClock(now func() time.Time) *circuitBreaker {
	b.now = now
	return b
}

func (b *circuitBreaker) get(provider string) *providerCircuit {
	c := b.m[provider]
	if c == nil {
		c = &providerCircuit{}
		b.m[provider] = c
	}
	return c
}

// allow reports whether a request may be sent to the provider.
//
// Closed circuits always allow. An open circuit allows nothing until its
// cooldown has elapsed, then lets exactly one probe through and holds the rest
// back until that probe reports — a broken probe must not turn into a stampede.
func (b *circuitBreaker) allow(provider string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.get(provider)
	switch c.state {
	case breakerOpen:
		if b.now().Sub(c.openedAt) < breakerCooldown {
			return false
		}
		if c.probeInFlight {
			return false
		}
		c.state = breakerHalfOpen
		c.probeInFlight = true
		return true
	case breakerHalfOpen:
		// The single probe is already out; everyone else waits for its verdict.
		return false
	default:
		return true
	}
}

// record applies the verdict of one attempt to the provider's circuit.
func (b *circuitBreaker) record(provider string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.get(provider)
	if ok {
		c.state = breakerClosed
		c.failures = 0
		c.probeInFlight = false
		return
	}
	// A failed probe re-opens the circuit with a fresh cooldown; it does not
	// count up from the old failure count, which the threshold already crossed.
	c.probeInFlight = false
	if c.state == breakerHalfOpen {
		c.state = breakerOpen
		c.openedAt = b.now()
		return
	}
	c.failures++
	if c.failures >= breakerFailureThreshold {
		c.state = breakerOpen
		c.openedAt = b.now()
	}
}

// providerCircuitView is one provider's circuit as the console shows it.
type providerCircuitView struct {
	Provider   string `json:"provider"`
	State      string `json:"state"`
	Failures   int    `json:"failures"`
	RetryInMS  int64  `json:"retry_in_ms"`
}

// circuits reports every provider the breaker has seen, so an operator can see
// why a channel is not being used.
func (b *circuitBreaker) circuits() []providerCircuitView {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]providerCircuitView, 0, len(b.m))
	now := b.now()
	for name, c := range b.m {
		v := providerCircuitView{Provider: name, State: c.state.String(), Failures: c.failures}
		if c.state == breakerOpen {
			if remaining := breakerCooldown - now.Sub(c.openedAt); remaining > 0 {
				v.RetryInMS = remaining.Milliseconds()
			}
		}
		out = append(out, v)
	}
	return out
}
