package midiport

// parser splits a MIDI byte stream into complete short messages: channel
// messages (with running status), system common messages, and real-time
// messages (clock, start, continue, stop, ...), which may appear inside
// another message and come out as they arrive. SysEx is dropped.
type parser struct {
	status byte
	buf    [3]byte
	rt     [1]byte
	n      int
	want   int
	sysex  bool
}

// messageLength is the byte count of the message a status byte starts.
func messageLength(status byte) int {
	switch {
	case status >= 0xF0:
		switch status {
		case 0xF2:
			return 3
		case 0xF1, 0xF3:
			return 2
		}
		return 1
	case status&0xF0 == 0xC0, status&0xF0 == 0xD0:
		return 2
	}
	return 3
}

func (p *parser) feed(data []byte, emit func([]byte)) {
	for _, b := range data {
		switch {
		case b >= 0xF8: // real-time, may appear anywhere
			p.rt[0] = b
			emit(p.rt[:])
			continue
		case b == 0xF0:
			p.sysex, p.status, p.n = true, 0, 0
			continue
		case b == 0xF7:
			p.sysex, p.status, p.n = false, 0, 0
			continue
		case b >= 0x80:
			p.sysex = false
			p.status, p.buf[0], p.n, p.want = b, b, 1, messageLength(b)
			if p.want == 1 {
				emit(p.buf[:1])
				p.status, p.n = 0, 0
			}
			continue
		}
		if p.sysex || p.status == 0 {
			continue
		}
		if p.n == 0 { // running status
			p.buf[0], p.n = p.status, 1
		}
		p.buf[p.n] = b
		p.n++
		if p.n == p.want {
			emit(p.buf[:p.n])
			p.n = 0
			if p.status >= 0xF0 { // system common messages end running status
				p.status = 0
			}
		}
	}
}
