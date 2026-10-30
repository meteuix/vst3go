package plugintest

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/th-release/vst3go/v2/pkg/ara"
)

// ARAHost is an ara.Host for tests of ARA plugins: it serves the samples
// of audio sources from memory, by persistent ID, and records the host
// notifications. Bind an instance to a document of it with
// plugin.OfflineSetup.ARA:
//
//	host := plugintest.NewARAHost()
//	host.SetAudio("take1", plugintest.Channels(2, plugintest.Sine(440, 48000, 48000, 0.5)))
//	doc := ara.NewDocument(myPlugin{}.ARA(), host, "song")
//	p := plugintest.New(t, myPlugin{}, plugin.OfflineSetup{ARA: doc})
type ARAHost struct {
	mu            sync.Mutex
	audio         map[string][][]float32
	notifications []ARANotification

	// Tempo and Bars are the content of every musical context.
	Tempo []ara.TempoEntry
	Bars  []ara.BarSignature
}

// ARANotification is a notification the plugin sent to the host. Kind is
// "progress", "source", "modification", "region" or "document"; ID is the
// persistent ID of the source or modification, or the region's host
// reference.
type ARANotification struct {
	Kind  string
	ID    string
	State ara.AnalysisState      // progress
	Flags ara.ContentUpdateFlags // content changes
}

// NewARAHost returns a host without audio.
func NewARAHost() *ARAHost { return &ARAHost{audio: map[string][][]float32{}} }

// SetAudio sets the samples of the sources with the persistent ID.
func (h *ARAHost) SetAudio(persistentID string, channels [][]float32) {
	h.mu.Lock()
	h.audio[persistentID] = channels
	h.mu.Unlock()
}

// TakeNotifications returns the notifications since the last call.
func (h *ARAHost) TakeNotifications() []ARANotification {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := h.notifications
	h.notifications = nil
	return n
}

func (h *ARAHost) record(n ARANotification) {
	h.mu.Lock()
	h.notifications = append(h.notifications, n)
	h.mu.Unlock()
}

type araReader struct{ data [][]float32 }

func (h *ARAHost) OpenAudio(s *ara.AudioSource) (ara.AudioReader, error) {
	h.mu.Lock()
	data, ok := h.audio[s.Properties().PersistentID]
	h.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("plugintest: no audio for source %q", s.Properties().PersistentID)
	}
	return araReader{data}, nil
}

// Read fills the buffers from position on; samples beyond the data are
// silent, as ARA hosts read them.
func (r araReader) Read(position int64, buffers [][]float32) error {
	for ch, b := range buffers {
		clear(b)
		if ch < len(r.data) && position < int64(len(r.data[ch])) {
			copy(b, r.data[ch][position:])
		}
	}
	return nil
}

func (araReader) Close() {}

func (h *ARAHost) MusicalContextContent(*ara.MusicalContext) ([]ara.TempoEntry, []ara.BarSignature) {
	return slices.Clone(h.Tempo), slices.Clone(h.Bars)
}

func (h *ARAHost) AnalysisProgress(s *ara.AudioSource, state ara.AnalysisState, value float32) {
	h.record(ARANotification{Kind: "progress", ID: s.Properties().PersistentID, State: state})
}

func (h *ARAHost) SourceContentChanged(s *ara.AudioSource, flags ara.ContentUpdateFlags) {
	h.record(ARANotification{Kind: "source", ID: s.Properties().PersistentID, Flags: flags})
}

func (h *ARAHost) ModificationContentChanged(m *ara.AudioModification, flags ara.ContentUpdateFlags) {
	h.record(ARANotification{Kind: "modification", ID: m.Properties().PersistentID, Flags: flags})
}

func (h *ARAHost) RegionContentChanged(r *ara.PlaybackRegion, flags ara.ContentUpdateFlags) {
	h.record(ARANotification{Kind: "region", ID: fmt.Sprint(r.HostRef()), Flags: flags})
}

func (h *ARAHost) DocumentDataChanged() { h.record(ARANotification{Kind: "document"}) }

// WaitARA waits until the samples of the sources are read and their
// analysis, if the plugin analyzes, finished; it fails the test on a read
// or analysis error and after ten seconds.
func WaitARA(t testing.TB, sources ...*ara.AudioSource) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, s := range sources {
		for {
			if err := s.Err(); err != nil {
				t.Fatalf("plugintest: audio source %q: %v", s.Properties().PersistentID, err)
			}
			if s.Audio() != nil && !analysisPending(s) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("plugintest: audio source %q was not read", s.Properties().PersistentID)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func analysisPending(s *ara.AudioSource) bool {
	for _, t := range []ara.ContentType{ara.ContentNotes, ara.ContentTempoEntries, ara.ContentBarSignatures} {
		if s.AnalysisIncomplete(t) {
			return true
		}
	}
	return false
}
