// Package perf provides lightweight, opt-in timing instrumentation.
//
// It is disabled by default and adds no measurable overhead in that state.
// Logging can be toggled at runtime via SetEnabled (e.g. from the Settings
// menu); the initial value is taken from the environment variable
// PROTO_VIEWER_PERF (1/true/yes). Logs go to stderr. Used to separate
// protoc/decode cost from UI render cost when diagnosing platform-specific
// slowness (e.g. software OpenGL on Windows).
package perf

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// slowThreshold — операции дольше этого помечаются [SLOW], чтобы в присланном
// логе легко находить «тормоза» (grep SLOW).
const slowThreshold = 80 * time.Millisecond

var (
	enabled  atomic.Bool
	initOnce sync.Once

	sinkMu sync.RWMutex
	sink   func(string)
)

// SetSink registers an optional callback that receives every formatted perf
// line (in addition to stderr). Used to mirror logs into the app's log panel.
// The callback may be invoked from any goroutine; keep it cheap / non-blocking.
func SetSink(fn func(string)) {
	sinkMu.Lock()
	sink = fn
	sinkMu.Unlock()
}

func emit(line string) {
	log.Print(line)
	sinkMu.RLock()
	fn := sink
	sinkMu.RUnlock()
	if fn != nil {
		fn(line)
	}
}

func initFromEnv() {
	initOnce.Do(func() {
		v := os.Getenv("PROTO_VIEWER_PERF")
		if v == "1" || v == "true" || v == "yes" {
			enabled.Store(true)
		}
	})
}

// Enabled reports whether perf logging is turned on.
func Enabled() bool {
	initFromEnv()
	return enabled.Load()
}

// SetEnabled turns perf logging on or off at runtime. Safe for concurrent use.
func SetEnabled(on bool) {
	initFromEnv()
	enabled.Store(on)
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
		d := time.Since(start)
		if d >= slowThreshold {
			emit(fmt.Sprintf("[perf][SLOW] %s took %s", label, d))
		} else {
			emit(fmt.Sprintf("[perf] %s took %s", label, d))
		}
	}
}

// LogEnv пишет в лог сведения об окружении (для разбора багов на чужой машине).
func LogEnv() {
	Log("env: os=%s arch=%s cpu=%d gomaxprocs=%d go=%s",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version())
}

// Log emits a perf message only when perf logging is enabled.
func Log(format string, args ...any) {
	if !Enabled() {
		return
	}
	emit("[perf] " + fmt.Sprintf(format, args...))
}
