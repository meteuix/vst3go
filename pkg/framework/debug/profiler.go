// Package debug provides debugging utilities for VST3 plugin development.
package debug

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Profiler collects timing statistics for named sections. It locks and
// allocates, so use it in tools and tests, not on the audio thread; see
// AudioProcessProfiler for that.
type Profiler struct {
	mu           sync.RWMutex
	measurements map[string]*Measurement
	enabled      atomic.Bool
	maxSamples   int
}

// Measurement holds timing statistics for a profiled section.
type Measurement struct {
	name        string
	count       uint64
	totalTime   time.Duration
	minTime     time.Duration
	maxTime     time.Duration
	lastTime    time.Duration
	samples     []time.Duration
	sampleIndex int
}

// DefaultProfiler is the global profiler instance.
var DefaultProfiler = NewProfiler(1000)

// NewProfiler creates a new profiler with the specified sample buffer size.
func NewProfiler(maxSamples int) *Profiler {
	p := &Profiler{
		measurements: make(map[string]*Measurement),
		maxSamples:   maxSamples,
	}
	p.enabled.Store(true)
	return p
}

// SetEnabled enables or disables profiling.
func (p *Profiler) SetEnabled(enabled bool) {
	p.enabled.Store(enabled)
}

// IsEnabled returns whether profiling is enabled.
func (p *Profiler) IsEnabled() bool {
	return p.enabled.Load()
}

// Start begins timing a named section.
func (p *Profiler) Start(name string) func() {
	if !p.enabled.Load() {
		return func() {} // No-op
	}

	start := time.Now()

	return func() {
		elapsed := time.Since(start)
		p.record(name, elapsed)
	}
}

// Time measures the execution time of a function.
func (p *Profiler) Time(name string, fn func()) {
	stop := p.Start(name)
	defer stop()
	fn()
}

// record stores a timing measurement.
func (p *Profiler) record(name string, elapsed time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	m, exists := p.measurements[name]
	if !exists {
		m = &Measurement{
			name:    name,
			minTime: elapsed,
			maxTime: elapsed,
			samples: make([]time.Duration, p.maxSamples),
		}
		p.measurements[name] = m
	}

	// Update statistics
	m.count++
	m.totalTime += elapsed
	m.lastTime = elapsed

	if elapsed < m.minTime {
		m.minTime = elapsed
	}
	if elapsed > m.maxTime {
		m.maxTime = elapsed
	}

	// Store sample
	m.samples[m.sampleIndex] = elapsed
	m.sampleIndex = (m.sampleIndex + 1) % p.maxSamples
}

// GetMeasurement returns the measurement for a named section.
func (p *Profiler) GetMeasurement(name string) (*Measurement, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	m, exists := p.measurements[name]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	copy := *m
	return &copy, true
}

// GetAllMeasurements returns all measurements.
func (p *Profiler) GetAllMeasurements() map[string]*Measurement {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]*Measurement)
	for k, v := range p.measurements {
		copy := *v
		result[k] = &copy
	}
	return result
}

// Reset clears all measurements.
func (p *Profiler) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.measurements = make(map[string]*Measurement)
}

// Report generates a performance report.
func (p *Profiler) Report() string {
	measurements := p.GetAllMeasurements()

	if len(measurements) == 0 {
		return "No measurements recorded"
	}

	report := "Performance Report:\n"
	report += "==================\n\n"

	for name, m := range measurements {
		avg := time.Duration(0)
		if m.count > 0 {
			avg = m.totalTime / time.Duration(m.count)
		}

		report += fmt.Sprintf("%s:\n", name)
		report += fmt.Sprintf("  Count:   %d\n", m.count)
		report += fmt.Sprintf("  Total:   %v\n", m.totalTime)
		report += fmt.Sprintf("  Average: %v\n", avg)
		report += fmt.Sprintf("  Min:     %v\n", m.minTime)
		report += fmt.Sprintf("  Max:     %v\n", m.maxTime)
		report += fmt.Sprintf("  Last:    %v\n", m.lastTime)
		report += "\n"
	}

	return report
}

// Measurement methods

// Average returns the average time for this measurement.
func (m *Measurement) Average() time.Duration {
	if m.count == 0 {
		return 0
	}
	return m.totalTime / time.Duration(m.count)
}

// Percentile calculates the given percentile from recent samples.
func (m *Measurement) Percentile(p float64) time.Duration {
	if m.count == 0 {
		return 0
	}

	// Collect valid samples
	validSamples := make([]time.Duration, 0, len(m.samples))
	for i := 0; i < len(m.samples) && i < int(m.count); i++ {
		if m.samples[i] > 0 {
			validSamples = append(validSamples, m.samples[i])
		}
	}

	if len(validSamples) == 0 {
		return 0
	}

	// Simple percentile calculation (not fully accurate but fast)
	index := int(float64(len(validSamples)-1) * p / 100.0)
	return validSamples[index]
}

// Global profiling functions

// Start begins timing a named section using the default profiler.
func Start(name string) func() {
	return DefaultProfiler.Start(name)
}

// Time measures the execution time of a function using the default profiler.
func Time(name string, fn func()) {
	DefaultProfiler.Time(name, fn)
}

// EnableProfiling enables the default profiler.
func EnableProfiling() {
	DefaultProfiler.SetEnabled(true)
}

// DisableProfiling disables the default profiler.
func DisableProfiling() {
	DefaultProfiler.SetEnabled(false)
}

// ResetProfiling clears all measurements in the default profiler.
func ResetProfiling() {
	DefaultProfiler.Reset()
}

// ProfilingReport returns a performance report from the default profiler.
func ProfilingReport() string {
	return DefaultProfiler.Report()
}

// AudioProcessProfiler measures how long each audio block takes compared
// with the block's duration. Unlike Profiler it is safe on the audio
// thread: Begin and End only read the clock and update atomics, and any
// other goroutine can read the statistics at the same time.
type AudioProcessProfiler struct {
	sampleRate float64

	blocks      atomic.Uint64
	totalNanos  atomic.Uint64 // time spent processing
	audioNanos  atomic.Uint64 // duration of the audio processed
	maxLoadBits atomic.Uint64 // highest single-block load, float64 bits
	lastLoadBit atomic.Uint64 // load of the last block, float64 bits
}

// NewAudioProcessProfiler creates a profiler for audio at sampleRate.
func NewAudioProcessProfiler(sampleRate float64) *AudioProcessProfiler {
	return &AudioProcessProfiler{sampleRate: sampleRate}
}

// Begin marks the start of a block; pass the result to End.
func (a *AudioProcessProfiler) Begin() time.Time {
	return time.Now()
}

// End records a block of numSamples samples that started at start.
func (a *AudioProcessProfiler) End(start time.Time, numSamples int) {
	elapsed := time.Since(start)
	audio := time.Duration(float64(numSamples) / a.sampleRate * float64(time.Second))
	a.blocks.Add(1)
	a.totalNanos.Add(uint64(elapsed))
	a.audioNanos.Add(uint64(audio))
	if audio <= 0 {
		return
	}
	load := float64(elapsed) / float64(audio) * 100
	a.lastLoadBit.Store(math.Float64bits(load))
	for {
		old := a.maxLoadBits.Load()
		if load <= math.Float64frombits(old) || a.maxLoadBits.CompareAndSwap(old, math.Float64bits(load)) {
			break
		}
	}
}

// Blocks returns the number of blocks recorded.
func (a *AudioProcessProfiler) Blocks() uint64 { return a.blocks.Load() }

// CPULoad returns the average processing time as a percentage of the audio
// duration processed.
func (a *AudioProcessProfiler) CPULoad() float64 {
	audio := a.audioNanos.Load()
	if audio == 0 {
		return 0
	}
	return float64(a.totalNanos.Load()) / float64(audio) * 100
}

// LastLoad returns the load of the most recent block, in percent.
func (a *AudioProcessProfiler) LastLoad() float64 {
	return math.Float64frombits(a.lastLoadBit.Load())
}

// MaxLoad returns the highest single-block load, in percent.
func (a *AudioProcessProfiler) MaxLoad() float64 {
	return math.Float64frombits(a.maxLoadBits.Load())
}

// Reset clears the statistics.
func (a *AudioProcessProfiler) Reset() {
	a.blocks.Store(0)
	a.totalNanos.Store(0)
	a.audioNanos.Store(0)
	a.maxLoadBits.Store(0)
	a.lastLoadBit.Store(0)
}

// AudioReport formats the statistics. It allocates; call it off the audio
// thread.
func (a *AudioProcessProfiler) AudioReport() string {
	return fmt.Sprintf("Audio Processing Stats:\n  Sample Rate:  %.0f Hz\n  Blocks:       %d\n  CPU Load:     %.2f%% average, %.2f%% peak\n",
		a.sampleRate, a.Blocks(), a.CPULoad(), a.MaxLoad())
}
