package sampler

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// SFZ is an instrument read by LoadSFZ.
type SFZ struct {
	Map *Map
	// Regions is the number of regions loaded and Samples the number of
	// distinct sample files they play.
	Regions, Samples int
	// Skipped counts regions that cannot play here: release triggers,
	// generated samples (sample=*sine) and empty key or velocity ranges.
	Skipped int
	// Ignored lists, sorted, the opcodes the file uses that LoadSFZ does
	// not implement; their regions play without them.
	Ignored []string
}

// LoadSFZ reads an SFZ instrument and the samples it names.
//
// It implements the <control>, <global>, <master>, <group> and <region>
// headers with their inheritance, #define and #include, and the opcodes
// sample, default_path, note_offset, octave_offset, lokey, hikey, key,
// lovel, hivel, pitch_keycenter (a note or "sample"), pitch_keytrack,
// tune, transpose, volume, amplitude, pan, offset, loop_mode, loop_start,
// loop_end, seq_length, seq_position, lorand, hirand and trigger. The
// plugin's envelope shapes every region; SFZ.Ignored names the rest.
// It allocates and reads files; call it off the audio thread.
func LoadSFZ(path string) (*SFZ, error) { return loadSFZ(path, LoadSample) }

// loadSFZ reads an SFZ instrument, loading its samples with load.
func loadSFZ(path string, load func(path string) (*Sample, error)) (*SFZ, error) {
	p := &sfzParser{
		load:    load,
		dir:     filepath.Dir(path),
		defines: map[string]string{},
		samples: map[string]*Sample{},
		ignored: map[string]bool{},
		scopes:  map[string]map[string]string{},
	}
	text, err := p.expand(path, 0)
	if err != nil {
		return nil, err
	}
	if err := p.parse(text); err != nil {
		return nil, err
	}
	if len(p.zones) == 0 {
		return nil, fmt.Errorf("%s: no playable regions", filepath.Base(path))
	}
	m, err := NewMap(p.zones...)
	if err != nil {
		return nil, err
	}
	sfz := &SFZ{Map: m, Regions: len(p.zones), Samples: len(p.samples), Skipped: p.skipped}
	for op := range p.ignored {
		sfz.Ignored = append(sfz.Ignored, op)
	}
	slices.Sort(sfz.Ignored)
	return sfz, nil
}

type sfzParser struct {
	load    func(path string) (*Sample, error)
	dir     string
	defines map[string]string
	samples map[string]*Sample
	ignored map[string]bool
	zones   []Zone
	skipped int

	// Opcodes of the current header at each level, and the current
	// header ("" before the first).
	scopes map[string]map[string]string
	header string
}

var (
	sfzComments = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)
	sfzTokens   = regexp.MustCompile(`<(\w+)>|(\w+)=`)
	sfzDefine   = regexp.MustCompile(`^#define\s+(\$\w+)\s+(.*?)\s*$`)
	sfzInclude  = regexp.MustCompile(`^#include\s+"([^"]+)"`)
	sfzNoteName = regexp.MustCompile(`^([a-gA-G])([#b]?)(-?\d+)$`)
)

// expand returns the file's text without comments, with #include
// replaced by the included file and #define names substituted.
func (p *sfzParser) expand(path string, depth int) (string, error) {
	if depth > 16 {
		return "", fmt.Errorf("%s: #include nested too deeply", filepath.Base(path))
	}
	raw, err := os.ReadFile(path) // #nosec G304 - the caller chooses the instrument
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for line := range strings.Lines(sfzComments.ReplaceAllString(string(raw), "")) {
		trimmed := strings.TrimSpace(line)
		if m := sfzDefine.FindStringSubmatch(trimmed); m != nil {
			p.defines[m[1]] = m[2]
			continue
		}
		if m := sfzInclude.FindStringSubmatch(trimmed); m != nil {
			// Included paths are relative to the instrument's folder.
			text, err := p.expand(filepath.Join(p.dir, filepath.FromSlash(strings.ReplaceAll(m[1], `\`, "/"))), depth+1)
			if err != nil {
				return "", err
			}
			out.WriteString(text)
			out.WriteByte('\n')
			continue
		}
		out.WriteString(p.substitute(line))
	}
	return out.String(), nil
}

// substitute replaces the #define names in line, longest first so $A does
// not match the start of $AB.
func (p *sfzParser) substitute(line string) string {
	if !strings.Contains(line, "$") {
		return line
	}
	names := make([]string, 0, len(p.defines))
	for name := range p.defines {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	for _, name := range names {
		line = strings.ReplaceAll(line, name, p.defines[name])
	}
	return line
}

// parse reads headers and opcodes. An opcode's value runs to the next
// opcode, header or line end; only sample keeps spaces (file names).
func (p *sfzParser) parse(text string) error {
	for line := range strings.Lines(text) {
		matches := sfzTokens.FindAllStringSubmatchIndex(line, -1)
		for i, m := range matches {
			if m[2] >= 0 {
				if err := p.open(line[m[2]:m[3]]); err != nil {
					return err
				}
				continue
			}
			end := len(line)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			op, value := line[m[4]:m[5]], strings.TrimSpace(line[m[1]:end])
			if op != "sample" {
				if f := strings.Fields(value); len(f) > 0 {
					value = f[0]
				}
			}
			if p.header == "" {
				return fmt.Errorf("sfz: opcode %s before any header", op)
			}
			if p.scopes[p.header] != nil {
				p.scopes[p.header][op] = value
			}
		}
	}
	return p.open("")
}

// open starts a header, finishing the region before it ("" finishes the
// file). Unknown headers (curve, effect, midi) and their opcodes are
// ignored.
func (p *sfzParser) open(header string) error {
	if p.header == "region" {
		if err := p.region(); err != nil {
			return err
		}
	}
	p.header = header
	// A header clears itself and the levels below it.
	levels := []string{"global", "master", "group", "region"}
	switch header {
	case "control":
		if p.scopes["control"] == nil {
			p.scopes["control"] = map[string]string{}
		}
	case "global", "master", "group", "region":
		for _, l := range levels[slices.Index(levels, header):] {
			p.scopes[l] = nil
		}
		p.scopes[header] = map[string]string{}
	case "":
	default:
		p.ignored["<"+header+">"] = true
	}
	return nil
}

// region turns the current region, with what it inherits, into a zone.
func (p *sfzParser) region() error {
	ops := map[string]string{}
	for _, l := range []string{"global", "master", "group", "region"} {
		for k, v := range p.scopes[l] {
			ops[k] = v
		}
	}
	control := p.scopes["control"]
	keyShift := p.intOp(control, "note_offset", 0) + 12*p.intOp(control, "octave_offset", 0)
	used := map[string]bool{}
	get := func(name string) (string, bool) {
		used[name] = true
		v, ok := ops[name]
		return v, ok
	}
	// Report the opcodes of loaded regions that went unread.
	loaded := false
	defer func() {
		for op := range ops {
			if loaded && !used[op] {
				p.ignored[op] = true
			}
		}
	}()

	name, ok := get("sample")
	trigger, _ := get("trigger")
	if !ok || strings.HasPrefix(name, "*") || trigger == "release" || trigger == "release_key" {
		p.skipped++
		return nil
	}
	if trigger != "" && trigger != "attack" {
		p.ignored["trigger="+trigger] = true
	}
	s, err := p.sample(control["default_path"] + name)
	if err != nil {
		return err
	}

	key := func(op string, def int) (int, error) {
		v, ok := get(op)
		if !ok {
			return def, nil
		}
		n, err := sfzNote(v)
		return n + keyShift, err
	}
	z := Zone{Sample: s}
	center, kt := 60.0, 100.0
	lo, hi := 0, 127
	if v, ok := get("key"); ok {
		n, err := sfzNote(v)
		if err != nil {
			return err
		}
		lo, hi, center = n+keyShift, n+keyShift, float64(n+keyShift)
	}
	if lo, err = key("lokey", lo); err != nil {
		return err
	}
	if hi, err = key("hikey", hi); err != nil {
		return err
	}
	if v, ok := get("pitch_keycenter"); ok {
		if v == "sample" {
			center = s.rootNote
		} else {
			n, err := sfzNote(v)
			if err != nil {
				return err
			}
			center = float64(n + keyShift)
		}
	}
	lv, hv := p.intOp(ops, "lovel", 1), p.intOp(ops, "hivel", 127)
	used["lovel"], used["hivel"] = true, true
	lo, hi = max(lo, 0), min(hi, 127)
	lv, hv = max(lv, 0), min(hv, 127)
	if lo > hi || lv > hv {
		p.skipped++
		return nil
	}
	z.LowKey, z.HighKey, z.LowVelocity, z.HighVelocity = uint8(lo), uint8(hi), uint8(lv), uint8(hv)

	num := func(op string, def float64) float64 {
		v, ok := get(op)
		if !ok {
			return def
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			p.ignored[op+"="+v] = true
			return def
		}
		return f
	}
	kt = num("pitch_keytrack", kt)
	if kt == 0 {
		z.FixedPitch = true
	} else {
		z.KeyTrack = kt
	}
	// The voice tunes from the sample's root; move that to the key center.
	z.Tune = num("tune", 0) + 100*num("transpose", 0) + (s.rootNote-center)*kt
	z.Gain = num("volume", 0)
	if amp := num("amplitude", 100); amp > 0 {
		z.Gain += 20 * math.Log10(amp/100)
	} else {
		z.Gain = math.Inf(-1)
	}
	z.Pan = num("pan", 0) / 100
	z.Offset = int(num("offset", 0))

	mode, ok := get("loop_mode")
	if !ok {
		mode, ok = get("loopmode")
	}
	switch mode {
	case "no_loop":
		z.Loop = LoopNone
	case "one_shot":
		z.Loop = OneShot
	case "loop_continuous":
		z.Loop = LoopContinuous
	case "loop_sustain":
		z.Loop = LoopSustain
	default:
		if ok {
			p.ignored["loop_mode="+mode] = true
		}
	}
	start, startOK := p.firstOp(get, "loop_start", "loopstart")
	end, endOK := p.firstOp(get, "loop_end", "loopend")
	if startOK || endOK {
		z.LoopStart, z.LoopEnd = s.loopStart, s.loopEnd
		if startOK {
			z.LoopStart = start
		}
		if endOK {
			z.LoopEnd = end + 1 // SFZ loop ends are inclusive
		}
		if z.LoopEnd <= z.LoopStart {
			z.LoopEnd = s.Len()
		}
	}

	z.SeqLength = int(num("seq_length", 1))
	z.SeqPosition = int(num("seq_position", 1))
	if z.SeqLength > 1 && (z.SeqPosition < 1 || z.SeqPosition > z.SeqLength) {
		p.skipped++
		return nil
	}
	_, lr := ops["lorand"]
	_, hr := ops["hirand"]
	if lr || hr {
		z.LowRand, z.HighRand = num("lorand", 0), num("hirand", 1)
	}
	p.zones = append(p.zones, z)
	loaded = true
	return nil
}

// firstOp returns the first of the named integer opcodes present.
func (p *sfzParser) firstOp(get func(string) (string, bool), names ...string) (int, bool) {
	for _, name := range names {
		if v, ok := get(name); ok {
			if n, err := strconv.Atoi(v); err == nil {
				return n, true
			}
			p.ignored[name+"="+v] = true
		}
	}
	return 0, false
}

// intOp returns an integer opcode of ops, or def.
func (p *sfzParser) intOp(ops map[string]string, name string, def int) int {
	v, ok := ops[name]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.ignored[name+"="+v] = true
		return def
	}
	return n
}

// sample loads a sample file once, relative to the instrument's folder.
func (p *sfzParser) sample(name string) (*Sample, error) {
	path := filepath.Join(p.dir, filepath.FromSlash(strings.ReplaceAll(name, `\`, "/")))
	if s, ok := p.samples[path]; ok {
		return s, nil
	}
	s, err := p.load(path)
	if err != nil {
		return nil, err
	}
	p.samples[path] = s
	return s, nil
}

// sfzNote parses a MIDI note number or name (c4 is 60, c#4 and db4 61).
func sfzNote(v string) (int, error) {
	if n, err := strconv.Atoi(v); err == nil {
		return n, nil
	}
	m := sfzNoteName.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("sfz: %q is not a note", v)
	}
	n := [7]int{9, 11, 0, 2, 4, 5, 7}[strings.ToLower(m[1])[0]-'a']
	switch m[2] {
	case "#":
		n++
	case "b":
		n--
	}
	octave, _ := strconv.Atoi(m[3])
	return (octave+1)*12 + n, nil
}
