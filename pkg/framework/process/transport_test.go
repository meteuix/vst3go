package process

import "testing"

func TestTransportInfoCalculations(t *testing.T) {
	info := &TransportInfo{
		HasMusicalTime:        true,
		HasTimeSignature:      true,
		TimeSigNumerator:      4,
		TimeSigDenominator:    4,
		ProjectTimeMusic:      5.5,
		BarPositionMusic:      4.0,
		HasTempo:              true,
		Tempo:                 120,
		HasBarPosition:        true,
		ProjectTimeSamples:    24000,
		ContinuousTimeSamples: 24000,
	}

	bars, beats := info.GetBarsBeats()
	if bars != 1 || beats != 1.5 {
		t.Fatalf("GetBarsBeats() = (%d, %f), want (1, 1.5)", bars, beats)
	}

	if got := info.GetSamplesPerBeat(48000); got != 24000 {
		t.Fatalf("GetSamplesPerBeat() = %f, want 24000", got)
	}

	if got := info.GetBeatPosition(); got != 0.5 {
		t.Fatalf("GetBeatPosition() = %f, want 0.5", got)
	}

	boundary := &TransportInfo{
		HasMusicalTime:   true,
		HasBarPosition:   true,
		ProjectTimeMusic: 4.03,
		BarPositionMusic: 4.0,
	}
	if !boundary.IsOnBeat(0.05) {
		t.Fatal("IsOnBeat(0.05) should be true near the beat boundary")
	}

	info.HasTempo = false
	if got := info.GetSamplesPerBeat(48000); got != 0 {
		t.Fatalf("GetSamplesPerBeat() without tempo = %f, want 0", got)
	}
}
