package preset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var testID = [16]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0x10, 0x32, 0x54, 0x76, 0x98, 0xBA, 0xDC, 0xFE}

func TestEncodeLayout(t *testing.T) {
	f := &File{ClassID: testID, Component: []byte("COMP"), MetaInfo: []byte("<x/>")}
	data := f.Encode()

	if string(data[:4]) != "VST3" || binary.LittleEndian.Uint32(data[4:]) != 1 {
		t.Fatalf("header = %q version %d", data[:4], binary.LittleEndian.Uint32(data[4:]))
	}
	if got := string(data[8:40]); got != ClassIDString(testID) {
		t.Fatalf("class ID = %s", got)
	}
	// Chunks follow the 48-byte header, the list comes last.
	if string(data[48:52]) != "COMP" || string(data[52:56]) != "<x/>" {
		t.Fatalf("chunk data = %q", data[48:56])
	}
	list := binary.LittleEndian.Uint64(data[40:])
	if list != 56 || string(data[56:60]) != "List" || binary.LittleEndian.Uint32(data[60:]) != 2 {
		t.Fatalf("list at %d: %q count %d", list, data[56:60], binary.LittleEndian.Uint32(data[60:]))
	}
	entry := data[64:84]
	if string(entry[:4]) != "Comp" || binary.LittleEndian.Uint64(entry[4:]) != 48 || binary.LittleEndian.Uint64(entry[12:]) != 4 {
		t.Fatalf("component entry = %q off %d size %d", entry[:4], binary.LittleEndian.Uint64(entry[4:]), binary.LittleEndian.Uint64(entry[12:]))
	}
	if string(data[84:88]) != "Info" || len(data) != 104 {
		t.Fatalf("second entry %q, file size %d", data[84:88], len(data))
	}
}

func TestRoundTrip(t *testing.T) {
	in := &File{ClassID: testID, Component: []byte{1, 2, 3}, Controller: []byte{4}, MetaInfo: MetaInfo{AttrPlugInName: "Synth"}.Encode()}
	out, err := Decode(in.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.ClassID != in.ClassID || !bytes.Equal(out.Component, in.Component) || !bytes.Equal(out.Controller, in.Controller) || !bytes.Equal(out.MetaInfo, in.MetaInfo) {
		t.Fatalf("round trip = %+v", out)
	}
	empty, err := Decode((&File{ClassID: testID}).Encode())
	if err != nil || len(empty.Component) != 0 {
		t.Fatalf("empty component state: %v", err)
	}
}

func TestDecodeRejectsBrokenFiles(t *testing.T) {
	good := (&File{ClassID: testID, Component: []byte("state")}).Encode()
	cases := map[string][]byte{
		"short":       good[:20],
		"magic":       append([]byte("VST2"), good[4:]...),
		"class id":    append(append(append([]byte{}, good[:8]...), bytes.Repeat([]byte("Z"), 32)...), good[40:]...),
		"list offset": append(append(append([]byte{}, good[:40]...), 0xFF, 0xFF, 0, 0, 0, 0, 0, 0), good[48:]...),
	}
	entry := binary.LittleEndian.Uint64(good[40:]) + 8 // first chunk list entry
	truncated := append([]byte{}, good...)
	binary.LittleEndian.PutUint64(truncated[entry+12:], 1<<40) // component chunk size
	cases["chunk size"] = truncated
	noComponent := append([]byte{}, good...)
	copy(noComponent[entry:], "Cont")
	cases["no component"] = noComponent
	for name, data := range cases {
		if _, err := Decode(data); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
}

func TestClassIDString(t *testing.T) {
	s := ClassIDString(testID)
	want := "0123456789ABCDEF1032547698BADCFE"
	if runtime.GOOS == "windows" {
		want = "67452301AB89EFCD1032547698BADCFE" // GUID fields of a COM TUID
	}
	if s != want {
		t.Fatalf("ClassIDString = %s, want %s", s, want)
	}
	back, err := ParseClassID(s)
	if err != nil || back != testID {
		t.Fatalf("ParseClassID = %x, %v", back, err)
	}
	if _, err := ParseClassID("0123"); err == nil {
		t.Fatal("short class ID accepted")
	}
}

func TestMetaInfo(t *testing.T) {
	m := MetaInfo{AttrPlugInName: "Synth", AttrInstrument: "Synth|Lead", "Custom": `a "b" <c>`}
	data := m.Encode()
	if !bytes.HasPrefix(data, []byte(`<?xml version="1.0" encoding="utf-8"?>`)) || !bytes.Contains(data, []byte(`<Attribute id="MediaType" value="VstPreset" type="string" flags="writeProtected"></Attribute>`)) {
		t.Fatalf("meta info XML:\n%s", data)
	}
	back, err := ParseMetaInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if back[AttrPlugInName] != "Synth" || back["Custom"] != `a "b" <c>` || back[AttrMediaType] != "VstPreset" {
		t.Fatalf("parsed = %v", back)
	}
	if !bytes.Equal(m.Encode(), data) {
		t.Fatal("encoding is not reproducible")
	}
}

func TestLibrary(t *testing.T) {
	dir := t.TempDir()
	f := &File{ClassID: testID, Component: []byte("x")}
	for _, name := range []string{"Lead", "bass/Fat Saw", "Bass/Sub"} {
		if _, err := Save(dir, name, f); err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	presets, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range presets {
		names = append(names, p.Name)
	}
	if len(names) != 3 || names[0] != "bass/Fat Saw" && names[0] != "Bass/Fat Saw" || names[2] != "Lead" {
		t.Fatalf("List = %v", names)
	}
	got, err := ReadFile(presets[2].Path)
	if err != nil || string(got.Component) != "x" {
		t.Fatalf("ReadFile: %v", err)
	}
	if missing, err := List(filepath.Join(dir, "none")); err != nil || len(missing) != 0 {
		t.Fatalf("missing folder: %v %v", missing, err)
	}
	for _, bad := range []string{"", "../x", "a/../../b", `a\b`, "con:x", "name.", "a//b"} {
		if _, err := PathFor(dir, bad); err == nil {
			t.Errorf("PathFor(%q) accepted", bad)
		}
	}
}

func TestUserDir(t *testing.T) {
	dir, err := UserDir("Acme", "Synth")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "Synth" || filepath.Base(filepath.Dir(dir)) != "Acme" {
		t.Fatalf("UserDir = %s", dir)
	}
	if _, err := UserDir("../x", "Synth"); err == nil {
		t.Fatal("UserDir accepted a path in the vendor")
	}
}
