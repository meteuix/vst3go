package debug

import (
	"strings"
	"testing"
	"time"
)

func TestProfiler(t *testing.T) {
	t.Run("BasicProfiling", func(t *testing.T) {
		p := NewProfiler(100)

		// Profile a section
		stop := p.Start("test")
		time.Sleep(10 * time.Millisecond)
		stop()

		// Check measurement
		m, exists := p.GetMeasurement("test")
		if !exists {
			t.Fatal("Measurement not found")
		}

		if m.count != 1 {
			t.Errorf("Expected count 1, got %d", m.count)
		}

		if m.lastTime < 10*time.Millisecond {
			t.Error("Timing seems too short")
		}
	})

	t.Run("MultipleRuns", func(t *testing.T) {
		p := NewProfiler(100)

		// Profile multiple times
		for i := 0; i < 5; i++ {
			stop := p.Start("multi")
			time.Sleep(time.Millisecond)
			stop()
		}

		m, exists := p.GetMeasurement("multi")
		if !exists {
			t.Fatal("Measurement not found")
		}

		if m.count != 5 {
			t.Errorf("Expected count 5, got %d", m.count)
		}

		// Check that min <= avg <= max
		avg := m.Average()
		if m.minTime > avg || avg > m.maxTime {
			t.Error("Invalid min/avg/max relationship")
		}
	})

	t.Run("TimeFunction", func(t *testing.T) {
		p := NewProfiler(100)

		called := false
		p.Time("function", func() {
			called = true
			time.Sleep(5 * time.Millisecond)
		})

		if !called {
			t.Error("Function not called")
		}

		m, exists := p.GetMeasurement("function")
		if !exists {
			t.Fatal("Measurement not found")
		}

		if m.count != 1 {
			t.Error("Expected one measurement")
		}
	})

	t.Run("Disabled", func(t *testing.T) {
		p := NewProfiler(100)
		p.SetEnabled(false)

		stop := p.Start("disabled")
		time.Sleep(time.Millisecond)
		stop()

		_, exists := p.GetMeasurement("disabled")
		if exists {
			t.Error("Measurement should not exist when disabled")
		}
	})

	t.Run("Reset", func(t *testing.T) {
		p := NewProfiler(100)

		stop := p.Start("reset")
		stop()

		p.Reset()

		measurements := p.GetAllMeasurements()
		if len(measurements) != 0 {
			t.Error("Measurements not cleared")
		}
	})

	t.Run("Report", func(t *testing.T) {
		p := NewProfiler(100)

		p.Time("task1", func() {
			time.Sleep(time.Millisecond)
		})
		p.Time("task2", func() {
			time.Sleep(2 * time.Millisecond)
		})

		report := p.Report()

		if !strings.Contains(report, "task1") {
			t.Error("Report missing task1")
		}
		if !strings.Contains(report, "task2") {
			t.Error("Report missing task2")
		}
		if !strings.Contains(report, "Count:") {
			t.Error("Report missing count")
		}
	})
}

func TestAudioProcessProfilerMeasuresRealTime(t *testing.T) {
	sampleRate := 48000.0
	bufferSize := 512
	p := NewAudioProcessProfiler(sampleRate)

	// Simulate 50% CPU load
	bufferDuration := time.Duration(float64(bufferSize) / sampleRate * float64(time.Second))
	for i := 0; i < 10; i++ {
		start := p.Begin()
		time.Sleep(bufferDuration / 2)
		p.End(start, bufferSize)
	}
	// time.Sleep never returns early, and rarely by much late.
	if cpuLoad := p.CPULoad(); cpuLoad < 50 || cpuLoad > 70 {
		t.Errorf("CPU load %.2f%%, want about 50%%", cpuLoad)
	}

	report := NewAudioProcessProfiler(44100).AudioReport()
	for _, want := range []string{"44100 Hz", "Blocks:       0", "CPU Load:"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}

func TestGlobalProfiler(t *testing.T) {
	// Reset global profiler
	ResetProfiling()
	EnableProfiling()

	// Use global functions
	stop := Start("global")
	time.Sleep(time.Millisecond)
	stop()

	Time("global2", func() {
		time.Sleep(time.Millisecond)
	})

	report := ProfilingReport()
	if !strings.Contains(report, "global") {
		t.Error("Global profiling not working")
	}
}

func BenchmarkProfiler(b *testing.B) {
	p := NewProfiler(1000)

	b.Run("StartStop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			stop := p.Start("bench")
			stop()
		}
	})

	b.Run("Disabled", func(b *testing.B) {
		p.SetEnabled(false)
		for i := 0; i < b.N; i++ {
			stop := p.Start("bench")
			stop()
		}
	})
}
