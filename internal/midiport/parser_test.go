package midiport

import (
	"reflect"
	"testing"
)

func TestParser(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   [][]byte
		want [][]byte
	}{
		{"note on and off", [][]byte{{0x90, 60, 100, 0x80, 60, 0}}, [][]byte{{0x90, 60, 100}, {0x80, 60, 0}}},
		{"running status", [][]byte{{0x91, 60, 100, 62, 90, 64, 0}}, [][]byte{{0x91, 60, 100}, {0x91, 62, 90}, {0x91, 64, 0}}},
		{"two-byte messages", [][]byte{{0xC3, 5, 7, 0xD0, 40}}, [][]byte{{0xC3, 5}, {0xC3, 7}, {0xD0, 40}}},
		{"split across packets", [][]byte{{0xB0, 1}, {64, 2}, {10}}, [][]byte{{0xB0, 1, 64}, {0xB0, 2, 10}}},
		{"real-time inside a message", [][]byte{{0x90, 0xF8, 60, 0xFE, 100}}, [][]byte{{0xF8}, {0xFE}, {0x90, 60, 100}}},
		{"clock and transport", [][]byte{{0xF2, 0, 1, 0xFA, 0xF8, 0xF8, 0xFC, 0xFB}}, [][]byte{{0xF2, 0, 1}, {0xFA}, {0xF8}, {0xF8}, {0xFC}, {0xFB}}},
		{"real-time keeps running status", [][]byte{{0x90, 60, 1, 0xF8, 62, 2}}, [][]byte{{0x90, 60, 1}, {0xF8}, {0x90, 62, 2}}},
		{"sysex dropped", [][]byte{{0xF0, 0x7E, 1, 2}, {3, 0xF7, 0x90, 60, 1}}, [][]byte{{0x90, 60, 1}}},
		{"sysex ends running status", [][]byte{{0x90, 60, 1, 0xF0, 1, 0xF7, 62, 2}}, [][]byte{{0x90, 60, 1}}},
		{"system common", [][]byte{{0xF2, 1, 2, 0xF3, 4, 0xF6, 5}}, [][]byte{{0xF2, 1, 2}, {0xF3, 4}, {0xF6}}},
		{"data without status", [][]byte{{60, 100, 0xE0, 0, 64}}, [][]byte{{0xE0, 0, 64}}},
	} {
		var p parser
		var got [][]byte
		for _, chunk := range tc.in {
			p.feed(chunk, func(msg []byte) { got = append(got, append([]byte(nil), msg...)) })
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got % x, want % x", tc.name, got, tc.want)
		}
	}
}
