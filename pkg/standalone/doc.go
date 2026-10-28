// Package standalone runs a vst3go plugin as an application: audio device
// I/O, MIDI ports, the plugin's editor in a window, and its state and
// device settings kept between runs.
//
// The same plugin package builds as a plugin and as an application. Its
// main function calls Main, which only runs in executables built with the
// standalone tag (plugin builds are c-shared libraries, whose main never
// runs):
//
//	func main() { standalone.Main() }
//
//	go build -tags standalone -o MyPlugin ./myplugin   (or scripts/build_standalone.sh)
//
// The application hosts the plugin through the plugin's own CLAP entry, so
// it runs the same runtime a CLAP host does. Audio goes through miniaudio
// (github.com/gen2brain/malgo: Core Audio, WASAPI, ALSA, PulseAudio, JACK),
// MIDI through RtMidi (gitlab.com/gomidi/midi/v2). See
// docs/plugin-capabilities.md for the flags and the platforms.
package standalone
