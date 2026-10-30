//go:build standalone

package standalone

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// With VST3GO_STANDALONE_GUI_CHECK=1 the test binary opens the application
// window with its toolbar and the settings window on the main thread
// (TestMain runs there) and checks that both pages load and talk to the
// application. It needs a desktop session: scripts/check_macos_editor.sh
// and scripts/docker_check_linux_editor.sh run it.
func TestMain(m *testing.M) {
	if os.Getenv("VST3GO_STANDALONE_GUI_CHECK") == "1" {
		if err := guiCheck(); err != nil {
			fmt.Fprintln(os.Stderr, "standalone gui check: FAIL:", err)
			os.Exit(1)
		}
		fmt.Println("standalone gui check: ok")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// tee records the messages to a panel and forwards them.
type tee struct {
	mu       sync.Mutex
	to       poster
	messages []any
}

func (t *tee) Post(message any) error {
	t.mu.Lock()
	t.messages = append(t.messages, message)
	t.mu.Unlock()
	return t.to.Post(message)
}

func (t *tee) has(match func(any) bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.messages {
		if match(m) {
			return true
		}
	}
	return false
}

func guiCheck() error {
	index, _, err := pluginIndex("Standalone Test Effect")
	if err != nil {
		return err
	}
	if err := createPlugin(index); err != nil {
		return err
	}
	defer destroyPlugin()
	dir, err := os.MkdirTemp("", "standalone-gui")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	a := &app{name: "GUI Check", dir: dir, log: os.Stderr, settings: Settings{Backend: "null", MIDIInput: "none", NoVirtualMIDI: true}}
	in, out := mainChannels()
	if a.audio, err = openAudio(a.settings, in, out); err != nil {
		return err
	}
	defer func() { a.audio.close() }()
	if err := a.startAudio(); err != nil {
		return err
	}
	defer a.stopAudio()

	if _, _, ok := a.openEditor(); !ok {
		return errors.New("no application window")
	}
	defer a.closeEditor()
	if a.toolbar == nil {
		return errors.New("no toolbar panel")
	}
	toolbar := &tee{to: a.toolbarPost}
	a.toolbarPost = toolbar
	a.openSettings()
	if a.settingsWindow == nil {
		return errors.New("no settings window")
	}
	settings := &tee{to: a.settingsPost}
	a.settingsPost = settings

	// The pages post "ready" once loaded; the application answers.
	loaded := func() bool {
		return toolbar.has(func(m any) bool { _, ok := m.(statusMessage); return ok }) &&
			toolbar.has(func(m any) bool { _, ok := m.(transportMessage); return ok }) &&
			settings.has(func(m any) bool { d, ok := m.(devicesMessage); return ok && len(d.Backends) > 0 })
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		deadline := time.After(15 * time.Second)
		for !loaded() {
			select {
			case <-deadline:
				a.quit.Store(true)
				return
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
		a.quit.Store(true)
	}()
	a.loop()
	if !loaded() {
		return errors.New("the toolbar and settings pages did not load and answer within 15 s")
	}
	return nil
}
