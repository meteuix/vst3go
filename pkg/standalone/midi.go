//go:build standalone

package standalone

import (
	"errors"
	"fmt"
	"strings"

	"github.com/th-release/vst3go/internal/midiport"
)

// midiPorts are the MIDI inputs feeding the plugin and the output its MIDI
// goes to.
type midiPorts struct {
	ins     []*midiport.Input
	names   []string
	virtual string
	out     *midiport.Output
	done    chan struct{}
}

func midiPortNames() (ins, outs []string, err error) {
	if ins, err = midiport.Inputs(); err != nil {
		return nil, nil, err
	}
	outs, err = midiport.Outputs()
	return ins, outs, err
}

// openMIDI opens the MIDI ports of the settings: inputs (for plugins with
// note input), a virtual input named after the plugin, and the output.
// Messages go to in and come from out.
func openMIDI(s Settings, name string, noteInput bool, in chan<- [3]byte, out <-chan [3]byte) (*midiPorts, error) {
	m := &midiPorts{}
	wanted := s.midiInputs()
	if !noteInput {
		wanted = nil
	}
	if len(wanted) == 0 && (!noteInput || s.NoVirtualMIDI) && s.MIDIOutput == "" {
		return m, nil
	}
	if err := midiport.Init(name); err != nil {
		return nil, err
	}
	receive := func(msg []byte) {
		if b, ok := channelMessage(msg); ok {
			select {
			case in <- b:
			default: // the audio thread is behind; drop rather than block
			}
		}
	}
	var errs []error
	if noteInput {
		if len(wanted) > 0 {
			ports, err := midiport.Inputs()
			if err != nil {
				errs = append(errs, err)
			}
			for _, p := range ports {
				if !matchPort(wanted, p) {
					continue
				}
				input, err := midiport.OpenInput(p, receive)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				m.ins = append(m.ins, input)
				m.names = append(m.names, p)
			}
		}
		if !s.NoVirtualMIDI {
			input, err := midiport.OpenVirtualInput(name, receive)
			switch {
			case errors.Is(err, midiport.ErrUnsupported):
			case err != nil:
				errs = append(errs, fmt.Errorf("virtual MIDI input: %w", err))
			default:
				m.ins = append(m.ins, input)
				m.virtual = name
			}
		}
	}
	if s.MIDIOutput != "" {
		if err := m.openOutput(s.MIDIOutput, out); err != nil {
			errs = append(errs, err)
		}
	}
	return m, errors.Join(errs...)
}

// openOutput opens the output port and sends the plugin's MIDI from out to
// it.
func (m *midiPorts) openOutput(name string, out <-chan [3]byte) error {
	o, err := midiport.OpenOutput(name)
	if err != nil {
		return err
	}
	m.out = o
	m.done = make(chan struct{})
	go func() {
		for {
			select {
			case b := <-out:
				_ = o.Send(b[:messageLength(b[0])])
			case <-m.done:
				return
			}
		}
	}()
	return nil
}

// channelMessage keeps channel voice messages (note, pressure, controller,
// program, pitch bend) as 3 bytes; the plugin gets no system messages.
func channelMessage(msg []byte) ([3]byte, bool) {
	var b [3]byte
	if len(msg) == 0 || msg[0] < 0x80 || msg[0] >= 0xF0 || len(msg) < messageLength(msg[0]) {
		return b, false
	}
	copy(b[:], msg[:messageLength(msg[0])])
	return b, true
}

// messageLength is the byte count of a channel voice message.
func messageLength(status byte) int {
	switch status & 0xF0 {
	case 0xC0, 0xD0:
		return 2
	}
	return 3
}

func matchPort(wanted []string, name string) bool {
	for _, w := range wanted {
		if w == "*" || strings.Contains(strings.ToLower(name), strings.ToLower(w)) {
			return true
		}
	}
	return false
}

func (m *midiPorts) close() {
	if m == nil {
		return
	}
	for _, p := range m.ins {
		p.Close()
	}
	if m.out != nil {
		close(m.done)
		m.out.Close()
	}
}

func (m *midiPorts) describe() string {
	if m == nil {
		return "no MIDI"
	}
	var parts []string
	if len(m.names) > 0 {
		parts = append(parts, "MIDI in "+strings.Join(m.names, ", "))
	}
	if m.virtual != "" {
		parts = append(parts, fmt.Sprintf("virtual MIDI in %q", m.virtual))
	}
	if m.out != nil {
		parts = append(parts, fmt.Sprintf("MIDI out %q", m.out.Name))
	}
	if len(parts) == 0 {
		return "no MIDI"
	}
	return strings.Join(parts, ", ")
}
