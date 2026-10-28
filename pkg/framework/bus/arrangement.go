package bus

import "math/bits"

// SpeakerArrangement is a VST3 speaker arrangement bitmask. Each set bit is
// one speaker, so the channel count of an arrangement is its population count.
type SpeakerArrangement uint64

// Individual speaker bits (subset of the VST3 speaker set).
const (
	SpeakerL   SpeakerArrangement = 1 << 0
	SpeakerR   SpeakerArrangement = 1 << 1
	SpeakerC   SpeakerArrangement = 1 << 2
	SpeakerLfe SpeakerArrangement = 1 << 3
	SpeakerLs  SpeakerArrangement = 1 << 4
	SpeakerRs  SpeakerArrangement = 1 << 5
	SpeakerLc  SpeakerArrangement = 1 << 6
	SpeakerRc  SpeakerArrangement = 1 << 7
	SpeakerS   SpeakerArrangement = 1 << 8
	SpeakerSl  SpeakerArrangement = 1 << 9
	SpeakerSr  SpeakerArrangement = 1 << 10
	SpeakerM   SpeakerArrangement = 1 << 19
)

// Common speaker arrangements, matching Steinberg::Vst::SpeakerArr.
const (
	ArrangementEmpty      SpeakerArrangement = 0
	ArrangementMono                          = SpeakerM
	ArrangementStereo                        = SpeakerL | SpeakerR
	Arrangement30Cine                        = SpeakerL | SpeakerR | SpeakerC
	Arrangement40Music                       = SpeakerL | SpeakerR | SpeakerLs | SpeakerRs
	Arrangement50                            = SpeakerL | SpeakerR | SpeakerC | SpeakerLs | SpeakerRs
	Arrangement51                            = Arrangement50 | SpeakerLfe
	Arrangement70Cine                        = Arrangement50 | SpeakerLc | SpeakerRc
	Arrangement71Cine                        = Arrangement70Cine | SpeakerLfe
	Arrangement70Music                       = Arrangement50 | SpeakerSl | SpeakerSr
	Arrangement71Music                       = Arrangement70Music | SpeakerLfe
	ArrangementQuad                          = Arrangement40Music
	ArrangementSurround51                    = Arrangement51
	ArrangementSurround71                    = Arrangement71Cine
)

// ChannelCount returns the number of channels in the arrangement.
func (a SpeakerArrangement) ChannelCount() int32 {
	return int32(bits.OnesCount64(uint64(a)))
}

// ArrangementForChannels returns the conventional arrangement for a channel
// count: mono, stereo, LRC, quad, 5.0, 5.1, 7.0, 7.1. Other counts use the
// first n speaker bits, which is a valid (if unnamed) VST3 arrangement.
func ArrangementForChannels(channels int32) SpeakerArrangement {
	switch channels {
	case 0:
		return ArrangementEmpty
	case 1:
		return ArrangementMono
	case 2:
		return ArrangementStereo
	case 3:
		return Arrangement30Cine
	case 4:
		return Arrangement40Music
	case 5:
		return Arrangement50
	case 6:
		return Arrangement51
	case 7:
		return Arrangement70Cine
	case 8:
		return Arrangement71Cine
	}
	if channels < 0 || channels > 64 {
		return ArrangementEmpty
	}
	if channels == 64 {
		return SpeakerArrangement(^uint64(0))
	}
	return SpeakerArrangement(uint64(1)<<uint(channels) - 1)
}
