package plugin

import (
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

var gcSink []byte

// gcCycles returns how many collections ran.
func gcCycles() uint32 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.NumGC
}

// allocate leaves mb MB of garbage.
func allocate(mb int) {
	for range mb {
		gcSink = make([]byte, 1<<20)
	}
	gcSink = nil
}

// settle runs a cycle and waits until its cleanup moved the limit.
func settle(t *testing.T) {
	t.Helper()
	gcSink = make([]byte, 64) // allocations to tell the cycle's mark apart
	gcSink = nil
	before := gcAllocsAt.Load()
	runtime.GC()
	for deadline := time.Now().Add(5 * time.Second); gcAllocsAt.Load() == before; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no cleanup after a cycle")
		}
	}
}

// TestGCHeadroom runs in a child process: the policy changes the whole
// runtime's collector settings.
func TestGCHeadroom(t *testing.T) {
	if os.Getenv("VST3GO_GC_CHILD") == "" {
		env := []string{"VST3GO_GC_CHILD=1", gcHeadroomEnv + "=16777216"}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GOGC=") && !strings.HasPrefix(kv, "GOMEMLIMIT=") && !strings.HasPrefix(kv, gcHeadroomEnv+"=") {
				env = append(env, kv)
			}
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestGCHeadroom$", "-test.v")
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "--- PASS: TestGCHeadroom") {
			t.Fatalf("child: %v\n%s", err, out)
		}
		return
	}

	runtime.GC()
	startGCPolicy()
	if prev := debug.SetGCPercent(-1); prev != -1 {
		t.Fatalf("GOGC %d, want off", prev)
	}
	limit := debug.SetMemoryLimit(-1)

	// Garbage under the headroom starts no cycle: with GOGC=100 and a
	// heap this small, 8 MB would start a few.
	start := gcCycles()
	allocate(8)
	if n := gcCycles() - start; n != 0 {
		t.Fatalf("%d cycles for 8 MB of garbage under 16 MB of headroom", n)
	}
	// Activation collects what is left over.
	collectBeforeProcessing()
	if gcCycles() == start {
		t.Fatal("activation did not collect 8 MB of garbage")
	}
	// Garbage beyond the headroom does start a cycle, which then gives the
	// next one the headroom again.
	start = gcCycles()
	allocate(64)
	if gcCycles() == start {
		t.Fatal("no cycle for 64 MB of garbage")
	}
	settle(t)
	if debug.SetMemoryLimit(-1) == limit {
		t.Fatal("the memory limit did not move")
	}
	start = gcCycles()
	allocate(8)
	if n := gcCycles() - start; n != 0 {
		t.Fatalf("%d cycles for 8 MB of garbage after the limit moved", n)
	}
	// Little garbage: activation does not collect.
	settle(t)
	start = gcCycles()
	collectBeforeProcessing()
	if n := gcCycles() - start; n != 0 {
		t.Fatalf("activation collected %d times without garbage", n)
	}
}
