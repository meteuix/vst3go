package analysis

import (
	"math"
	"sync"
	"testing"
)

func TestPublishedHandsOverTheNewestReading(t *testing.T) {
	var p published[int]
	p.init(func(v *int) { *v = -1 })
	if got := p.get(); got != -1 {
		t.Fatalf("before publishing: %d", got)
	}
	for i := range 10 {
		*p.write() = i
		p.publish()
		if i%3 == 0 {
			if got := p.get(); got != i {
				t.Fatalf("after publishing %d: %d", i, got)
			}
		}
	}
	// Readers keep the newest value until the writer publishes again.
	if p.get() != 9 || p.get() != 9 {
		t.Fatal("reading twice lost the value")
	}
}

func TestPublishedWriterIgnoresReaders(t *testing.T) {
	var p published[[4]int]
	p.init(func(*[4]int) {})
	// A reader holding the lock does not hold up the writer.
	p.readMu.Lock()
	for i := range 100 {
		*p.write() = [4]int{i, i, i, i}
		p.publish()
	}
	p.readMu.Unlock()
	if got := p.get(); got != [4]int{99, 99, 99, 99} {
		t.Fatalf("got %v", got)
	}

	// Readers never see a half-written value (go test -race checks the
	// handover).
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := p.get()
				if v[0] != v[1] || v[1] != v[2] || v[2] != v[3] {
					t.Errorf("torn reading %v", v)
					return
				}
				if v[0] < last {
					t.Errorf("reading went back from %d to %d", last, v[0])
					return
				}
				last = v[0]
			}
		}()
	}
	for i := 100; i < 20000; i++ {
		*p.write() = [4]int{i, i, i, i}
		p.publish()
	}
	close(stop)
	wg.Wait()
}

func TestMetersReadConcurrentlyWithProcess(t *testing.T) {
	const rate = 48000
	peak := NewPeakMeter(rate)
	rms := NewRMSMeter(4800)
	lufs := NewLUFSMeter(rate, 2)
	spec := NewSpectrumAnalyzer(1024, rate, HannWindow)
	scope := NewPhaseScope(512)
	field := NewStereoFieldAnalyzer(1024, rate)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if p := peak.GetPeak(); p < 0 || p > 0.5+1e-9 {
				t.Errorf("peak %v", p)
				return
			}
			_ = rms.GetRMSDB()
			_ = lufs.GetMomentaryLUFS()
			_ = lufs.GetIntegratedLUFS()
			_ = spec.GetSpectrumDB()
			_, _ = spec.GetPeakFrequency()
			_, _ = scope.GetPoints()
			_ = scope.GetStatistics()
			_ = field.GetAnalysis()
		}
	}()

	l := make([]float64, 512)
	r := make([]float64, 512)
	inter := make([]float64, 1024)
	phase := 0.0
	for range 400 {
		for i := range l {
			l[i] = 0.5 * math.Sin(phase)
			r[i] = 0.25 * math.Sin(phase)
			inter[2*i], inter[2*i+1] = l[i], r[i]
			phase += 2 * math.Pi * 1000 / rate
		}
		peak.Process(l)
		rms.Process(l)
		lufs.Process(inter)
		spec.Process(l)
		scope.Process(l, r)
		field.Process(l, r)
	}
	close(stop)
	wg.Wait()

	if got := peak.GetPeakDB(); math.Abs(got-20*math.Log10(0.5)) > 0.01 {
		t.Errorf("peak %.2f dB", got)
	}
	if f, _ := spec.GetPeakFrequency(); math.Abs(f-1000) > rate/1024.0 {
		t.Errorf("spectrum peak at %.1f Hz", f)
	}
	if c := field.GetAnalysis().Correlation; c < 0.99 {
		t.Errorf("correlation %.3f for in-phase channels", c)
	}
}
