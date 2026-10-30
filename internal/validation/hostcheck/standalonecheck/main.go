//go:build standalone

// Command standalonecheck runs a standalone application of a MIDI effect
// (the transpose plugin) the way a user does and plays it over MIDI: it
// opens a virtual MIDI input of its own, starts the application with the
// null audio device and its MIDI output on that input, plays the
// application's virtual MIDI input, and checks that the messages come back
// through the plugin. Then it stops the application with SIGTERM, which
// must save the state and exit 0. Needs Core MIDI or ALSA sequencer
// virtual ports (macOS, Linux with snd-seq).
//
// usage: standalonecheck <MidiFx app binary>
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/meteuix/vst3go/v2/internal/midiport"
)

const (
	checkPort = "vst3go standalonecheck"
	appPort   = "VST3Go Transpose"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: standalonecheck <MidiFx app binary>")
	}
	check(midiport.Init("standalonecheck"), "MIDI")
	received := make(chan []byte, 16)
	in, err := midiport.OpenVirtualInput(checkPort, func(msg []byte) { received <- append([]byte(nil), msg...) })
	check(err, "virtual MIDI input")
	defer in.Close()

	dir, err := os.MkdirTemp("", "standalonecheck")
	check(err, "temp dir")
	defer os.RemoveAll(dir)
	var log bytes.Buffer
	app := exec.Command(os.Args[1], "-headless", "-backend", "null", "-config", dir, "-midi-in", "none", "-midi-out", checkPort)
	app.Stdout, app.Stderr = &log, &log
	check(app.Start(), "start the application")
	exited := make(chan error, 1)
	go func() { exited <- app.Wait() }()

	out := waitForOutput(exited, &log)
	defer out.Close()
	messages := [][]byte{{0x90, 60, 100}, {0xB0, 1, 64}, {0x80, 60, 0}}
	for _, m := range messages {
		check(out.Send(m), "send")
	}
	for _, want := range messages {
		select {
		case got := <-received:
			// Note offs may come back with a release velocity.
			if !bytes.Equal(got[:2], want[:2]) || (want[0] != 0x80 && !bytes.Equal(got, want)) {
				fail("received % x, want % x\n%s", got, want, log.String())
			}
		case <-time.After(3 * time.Second):
			fail("no MIDI back for % x through the plugin\n%s", want, log.String())
		}
	}

	check(app.Process.Signal(syscall.SIGTERM), "SIGTERM")
	select {
	case err := <-exited:
		check(err, "application exit\n"+log.String())
	case <-time.After(5 * time.Second):
		_ = app.Process.Kill()
		fail("the application did not quit on SIGTERM\n%s", log.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "standalone.state")); err != nil {
		fail("state not saved on quit: %v", err)
	}
	if !strings.Contains(log.String(), fmt.Sprintf("virtual MIDI in %q", appPort)) {
		fail("log: %s", log.String())
	}
	fmt.Println("standalone ok:", strings.TrimSpace(log.String()))
}

// waitForOutput waits for the application's virtual MIDI input, which other
// applications see as an output port.
func waitForOutput(exited <-chan error, log *bytes.Buffer) *midiport.Output {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case err := <-exited:
			fail("the application exited: %v\n%s", err, log.String())
		default:
		}
		outs, err := midiport.Outputs()
		check(err, "MIDI outputs")
		for _, name := range outs {
			if strings.Contains(name, appPort) {
				out, err := midiport.OpenOutput(name)
				check(err, "open the application's MIDI input")
				return out
			}
		}
	}
	fail("no MIDI port %q\n%s", appPort, log.String())
	return nil
}

func check(err error, what string) {
	if err != nil {
		fail("%s: %v", what, err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL "+format+"\n", args...)
	os.Exit(1)
}
