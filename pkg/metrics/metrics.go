package metrics

import (
	"log"
	"runtime"
	"time"
)

// Metrics holds performance metrics for micropod operations
type Metrics struct {
	StartTime     time.Time
	LastOperation string
	LastDuration  time.Duration
	VMCount       int
	MemoryUsageMB float64
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

// LogOperation logs the duration of an operation
func (m *Metrics) LogOperation(operation string, start time.Time) {
	duration := time.Since(start)
	m.LastOperation = operation
	m.LastDuration = duration

	log.Printf("⏱️  %s completed in %v", operation, duration)

	// Log performance warnings
	if duration > 30*time.Second {
		log.Printf("⚠️  %s took longer than expected: %v", operation, duration)
	}
}

// UpdateVMCount updates the current VM count
func (m *Metrics) UpdateVMCount(count int) {
	m.VMCount = count
}

// UpdateMemoryUsage updates memory usage metrics
func (m *Metrics) UpdateMemoryUsage() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	m.MemoryUsageMB = float64(mem.Alloc) / 1024 / 1024
}

// LogResourceUsage logs current resource usage
func (m *Metrics) LogResourceUsage() {
	m.UpdateMemoryUsage()

	uptime := time.Since(m.StartTime)
	log.Printf("📊 Resource Usage:")
	log.Printf("   Uptime: %v", uptime)
	log.Printf("   Active VMs: %d", m.VMCount)
	log.Printf("   Memory Usage: %.2f MB", m.MemoryUsageMB)
	log.Printf("   Last Operation: %s (%v)", m.LastOperation, m.LastDuration)
}

// LogStartupBanner logs a startup banner with system info
func LogStartupBanner() {
	log.Printf("🚀 Micropod Agent Architecture")
	log.Printf("   Version: v0.2.0-agent")
	log.Printf("   Go Version: %s", runtime.Version())
	log.Printf("   Architecture: %s/%s", runtime.GOOS, runtime.GOARCH)
	log.Printf("   CPUs: %d", runtime.NumCPU())

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	log.Printf("   Available Memory: %.2f MB", float64(mem.Sys)/1024/1024)
}

// Timer provides a simple way to measure operation duration
type Timer struct {
	name  string
	start time.Time
}

// NewTimer creates a new timer for an operation
func NewTimer(operation string) *Timer {
	log.Printf("▶️  Starting %s...", operation)
	return &Timer{
		name:  operation,
		start: time.Now(),
	}
}

// Stop stops the timer and logs the duration
func (t *Timer) Stop() time.Duration {
	duration := time.Since(t.start)

	// Use different emojis based on duration
	var emoji string
	switch {
	case duration < 1*time.Second:
		emoji = "⚡"
	case duration < 5*time.Second:
		emoji = "✅"
	case duration < 30*time.Second:
		emoji = "⏳"
	default:
		emoji = "🐌"
	}

	log.Printf("%s %s completed in %v", emoji, t.name, duration)
	return duration
}
