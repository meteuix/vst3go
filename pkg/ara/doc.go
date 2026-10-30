// Package ara is the plugin side of ARA (Audio Random Access, by
// Celemony): plugins that edit and render the audio of the host's clips
// instead of the live signal, with access to the whole recording, such as
// pitch and timing editors, audio-to-MIDI analysis and clip-based effects.
//
// A plugin supports ARA by implementing plugin.ARAPlugin, which returns a
// Config. For every ARA document in the host, the runtime creates a
// Document and the plugin's Controller of it, and mirrors the host's model
// in it: audio sources (the recordings, whose samples the runtime reads
// into memory as SourceAudio), the plugin's audio modifications of them,
// the playback regions that play those in the song, region sequences
// (tracks) and musical contexts (tempo maps). The controller stores its
// data in the host's document (ModificationArchiver and the other
// archivers), finds content such as notes (Analyzer), and reacts to edits.
//
// Plugin instances are bound to a document (plugin.ARAProcessor) and render
// the playback regions the host assigns them: on the audio thread,
// Binding.Playback returns a snapshot of them, and RegionPlayback.Mix (or
// Span, for custom rendering) reads their audio for the current block.
// The modification state set with AudioModification.SetState reaches the
// snapshot, so an editor can change how a clip sounds.
//
// The Host interface lets tests drive a Document directly, without a host.
package ara
