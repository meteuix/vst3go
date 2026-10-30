package ara

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// archiveMagic starts every archive the runtime writes. The records after
// it hold the controller's data and the sources' algorithms: a kind byte,
// the object's persistent ID and the data, each length-prefixed with a
// uvarint.
const archiveMagic = "VST3GO-ARA-ARCHIVE\x00\x01"

const (
	recordDocument     = 'D'
	recordSource       = 'S'
	recordModification = 'M'
	recordAlgorithm    = 'A' // data: the Algorithm.ID of a source
)

// StoreFilter selects what Store writes; a nil filter writes everything.
type StoreFilter struct {
	// Document includes the controller's document data.
	Document      bool
	Sources       []*AudioSource
	Modifications []*AudioModification
}

// RestoreFilter selects what Restore reads; a nil filter reads everything
// into the objects with the archived persistent IDs.
type RestoreFilter struct {
	// Document includes the controller's document data.
	Document bool
	// Sources and Modifications map the persistent IDs in the archive to
	// restore to the persistent IDs of the objects to restore them into.
	Sources       map[string]string
	Modifications map[string]string
}

// Store writes the controller's data of the selected objects. The host
// does not call it while editing.
func (d *Document) Store(filter *StoreFilter) ([]byte, error) {
	sources, mods := d.sources, []*AudioModification(nil)
	for _, s := range d.sources {
		mods = append(mods, s.modifications...)
	}
	document := true
	if filter != nil {
		document, sources, mods = filter.Document, filter.Sources, filter.Modifications
	}
	var buf bytes.Buffer
	buf.WriteString(archiveMagic)
	if a, ok := d.controller.(DocumentArchiver); ok && document {
		data, err := a.StoreDocument()
		if err != nil {
			return nil, fmt.Errorf("ara: storing the document: %w", err)
		}
		writeRecord(&buf, recordDocument, "", data)
	}
	// The algorithm goes first: restoring it drops the content that a
	// source record restores after it.
	if algorithms := d.config.Algorithms; len(algorithms) > 0 {
		for _, s := range sources {
			writeRecord(&buf, recordAlgorithm, s.Properties().PersistentID, []byte(algorithms[s.Algorithm()].ID))
		}
	}
	if a, ok := d.controller.(SourceArchiver); ok {
		for _, s := range sources {
			data, err := a.StoreSource(s)
			if err != nil {
				return nil, fmt.Errorf("ara: storing audio source %q: %w", s.Properties().PersistentID, err)
			}
			if data != nil {
				writeRecord(&buf, recordSource, s.Properties().PersistentID, data)
			}
		}
	}
	if a, ok := d.controller.(ModificationArchiver); ok {
		for _, m := range mods {
			data, err := a.StoreModification(m)
			if err != nil {
				return nil, fmt.Errorf("ara: storing audio modification %q: %w", m.props.PersistentID, err)
			}
			if data != nil {
				writeRecord(&buf, recordModification, m.props.PersistentID, data)
			}
		}
	}
	return buf.Bytes(), nil
}

func writeRecord(buf *bytes.Buffer, kind byte, id string, data []byte) {
	buf.WriteByte(kind)
	buf.Write(binary.AppendUvarint(nil, uint64(len(id))))
	buf.WriteString(id)
	buf.Write(binary.AppendUvarint(nil, uint64(len(data))))
	buf.Write(data)
}

// Restore reads an archive Store wrote into the objects with the archived
// persistent IDs, inside an editing session. Records without a matching
// object are skipped. It restores every record it can and returns the
// first error.
func (d *Document) Restore(archive []byte, filter *RestoreFilter) error {
	if !bytes.HasPrefix(archive, []byte(archiveMagic)) {
		return errors.New("ara: not an archive of this plugin")
	}
	d.restoring = true
	defer func() { d.restoring = false }()
	var first error
	fail := func(err error) {
		if first == nil {
			first = err
		}
	}
	rest := archive[len(archiveMagic):]
	for len(rest) > 0 {
		kind := rest[0]
		id, data, next, err := readRecord(rest[1:])
		if err != nil {
			fail(err)
			break
		}
		rest = next
		switch kind {
		case recordDocument:
			a, ok := d.controller.(DocumentArchiver)
			if ok && (filter == nil || filter.Document) {
				if err := a.RestoreDocument(data); err != nil {
					fail(fmt.Errorf("ara: restoring the document: %w", err))
				}
			}
		case recordSource:
			a, ok := d.controller.(SourceArchiver)
			if !ok {
				continue
			}
			if s := d.findSource(mapID(id, filter, true)); s != nil {
				if err := a.RestoreSource(s, data); err != nil {
					fail(fmt.Errorf("ara: restoring audio source %q: %w", id, err))
				}
			}
		case recordAlgorithm:
			// An algorithm this version does not have keeps the source's.
			i := slices.IndexFunc(d.config.Algorithms, func(a Algorithm) bool { return a.ID == string(data) })
			if s := d.findSource(mapID(id, filter, true)); s != nil && i >= 0 {
				s.SetAlgorithm(i)
			}
		case recordModification:
			a, ok := d.controller.(ModificationArchiver)
			if !ok {
				continue
			}
			if m := d.findModification(mapID(id, filter, false)); m != nil {
				if err := a.RestoreModification(m, data); err != nil {
					fail(fmt.Errorf("ara: restoring audio modification %q: %w", id, err))
				}
			}
		default:
			fail(fmt.Errorf("ara: unknown archive record %q", kind))
			rest = nil
		}
	}
	d.update()
	return first
}

// mapID returns the current persistent ID an archived one restores into,
// "" when the filter leaves it out.
func mapID(id string, filter *RestoreFilter, source bool) string {
	if filter == nil {
		return id
	}
	ids := filter.Modifications
	if source {
		ids = filter.Sources
	}
	if current, ok := ids[id]; ok {
		return current
	}
	return ""
}

func readRecord(b []byte) (id string, data, rest []byte, err error) {
	n, k := binary.Uvarint(b)
	if k <= 0 || n > uint64(len(b)-k) {
		return "", nil, nil, errors.New("ara: corrupt archive")
	}
	id, b = string(b[k:k+int(n)]), b[k+int(n):]
	n, k = binary.Uvarint(b)
	if k <= 0 || n > uint64(len(b)-k) {
		return "", nil, nil, errors.New("ara: corrupt archive")
	}
	return id, b[k : k+int(n)], b[k+int(n):], nil
}

func (d *Document) findSource(id string) *AudioSource {
	if id == "" {
		return nil
	}
	for _, s := range d.sources {
		if s.Properties().PersistentID == id {
			return s
		}
	}
	return nil
}

func (d *Document) findModification(id string) *AudioModification {
	if id == "" {
		return nil
	}
	for _, s := range d.sources {
		for _, m := range s.modifications {
			if m.props.PersistentID == id {
				return m
			}
		}
	}
	return nil
}
