package state

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

func TestManagerRejectsNilRegistry(t *testing.T) {
	m := NewManager(nil)

	if err := m.Save(&bytes.Buffer{}); !errors.Is(err, ErrNilRegistry) {
		t.Fatalf("Save() error = %v, want ErrNilRegistry", err)
	}

	if err := m.Load(bytes.NewReader([]byte("VST3GO"))); !errors.Is(err, ErrNilRegistry) {
		t.Fatalf("Load() error = %v, want ErrNilRegistry", err)
	}
}

func TestManagerSaveLoadRoundTrip(t *testing.T) {
	reg := param.NewRegistry()
	p := &param.Parameter{ID: 1, Name: "Gain", Min: -12, Max: 12}
	p.SetPlain(6)
	if err := reg.Add(p); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	var buf bytes.Buffer
	m := NewManager(reg)
	if err := m.Save(&buf); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	p.SetPlain(-6)
	if err := m.Load(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := p.GetPlain(); got != 6 {
		t.Fatalf("GetPlain() after Load = %f, want 6", got)
	}
}

func TestManagerSaveLoadCustomState(t *testing.T) {
	reg := param.NewRegistry()
	p := &param.Parameter{ID: 1, Name: "Gain", Min: 0, Max: 1}
	p.SetNormalized(0.25)
	if err := reg.Add(p); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	const customPayload = "editor-snapshot"
	var loadedPayload string

	m := NewManager(reg)
	m.SetCustomSaveFunc(func(w io.Writer) error {
		_, err := io.WriteString(w, customPayload)
		return err
	})
	m.SetCustomLoadFunc(func(r io.Reader) error {
		payload, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		loadedPayload = string(payload)
		return nil
	})

	var buf bytes.Buffer
	if err := m.Save(&buf); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	p.SetNormalized(0.75)
	if err := m.Load(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loadedPayload != customPayload {
		t.Fatalf("loadedPayload = %q, want %q", loadedPayload, customPayload)
	}

	if got := p.GetNormalized(); got != 0.25 {
		t.Fatalf("GetNormalized() after Load = %f, want 0.25", got)
	}
}
