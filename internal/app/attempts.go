package app

import (
	"context"
	"sync"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// attemptRecorder collects the Routing Attempts of one request so the logging
// wrapper can persist them in a single place after the response is written.
//
// It travels in the request context rather than being returned up the call
// stack because the data plane is reached through an http.Handler: the wrapper
// that owns the Request Record is above the handler, not below it. A pointer in
// the context lets the handler append and the wrapper read without either one
// knowing the other's shape. The zero value is not usable; use newAttemptRecorder.
type attemptRecorder struct {
	mu       sync.Mutex
	attempts []model.RequestAttempt
}

func newAttemptRecorder() *attemptRecorder { return &attemptRecorder{} }

func (a *attemptRecorder) record(attempt model.RequestAttempt) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts = append(a.attempts, attempt)
}

func (a *attemptRecorder) list() []model.RequestAttempt {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]model.RequestAttempt(nil), a.attempts...)
}

type attemptRecorderKey struct{}

// withAttemptRecorder attaches a recorder to the context.
func withAttemptRecorder(ctx context.Context, a *attemptRecorder) context.Context {
	return context.WithValue(ctx, attemptRecorderKey{}, a)
}

// attemptRecorderFrom returns the recorder in the context, or nil when none is
// attached — a data plane reached without going through the logging wrapper (a
// unit test, or the connectivity probe) simply records nothing.
func attemptRecorderFrom(ctx context.Context) *attemptRecorder {
	a, _ := ctx.Value(attemptRecorderKey{}).(*attemptRecorder)
	return a
}
