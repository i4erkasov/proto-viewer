// Package perf provides lightweight, opt-in timing instrumentation.
//
// It is disabled by default and adds no measurable overhead in that state.
// Set the environment variable PROTO_VIEWER_PERF=1 to enable timing logs to
// stderr. This is used to separate protoc/decode cost from UI render cost when
// diagnosing platform-specific slowness (e.g. software OpenGL on Windows).
package perf

import (
	"log"
	"os"
	"sync"
	"time"
)

var (
	enabledOnce sync.Once
	enabled     bool
)

// Enabled reports whether perf logging is turned on.
func Enabled() bool {
	enabledOnce.Do(func() {
		v := os.Getenv("PROTO_VIEWER_PERF")
		enabled = v == "1" || v == "true" || v == "yes"
	})
	return enabled
}

// Track returns a function that, when called, logs the elapsed time since
// Track was invoked under the given label. Intended for `defer perf.Track(...)()`.
// When perf logging is disabled it returns a no-op closure.
func Track(label string) func() {
	if !Enabled() {
		return func() {}
	}
	start := time.Now()
	return func() {
		log.Printf("[perf] %s took %s", label, time.Since(start))
	}
}

// Log emits a perf message only when perf logging is enabled.
func Log(format string, args ...any) {
	if !Enabled() {
		return
	}
	log.Printf("[perf] "+format, args...)
}
