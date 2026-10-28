package process

// TransportInfo provides musical timing and transport state information
type TransportInfo struct {
	// Transport state
	IsPlaying   bool // Transport is playing
	IsRecording bool // Transport is recording
	IsCycling   bool // Loop/cycle is active

	// Tempo and timing
	Tempo              float64 // Current tempo in BPM (0 if not available)
	TimeSigNumerator   int32   // Time signature numerator (e.g., 4 for 4/4)
	TimeSigDenominator int32   // Time signature denominator (e.g., 4 for 4/4)

	// Musical position
	ProjectTimeMusic float64 // Musical position in quarter notes
	BarPositionMusic float64 // Bar position in quarter notes

	// Cycle/loop points
	CycleStartMusic float64 // Cycle start in quarter notes
	CycleEndMusic   float64 // Cycle end in quarter notes

	// Sample position
	ProjectTimeSamples    int64 // Project time in samples
	ContinuousTimeSamples int64 // Continuous time in samples (doesn't reset on loop)

	// Clock information
	SamplesToNextClock int32 // Samples until next clock/beat

	// Validity flags
	HasTempo         bool // Tempo field is valid
	HasTimeSignature bool // Time signature fields are valid
	HasMusicalTime   bool // Musical time fields are valid
	HasBarPosition   bool // Bar position is valid
	HasCycle         bool // Cycle points are valid
}

// GetBarsBeats returns the current position in bars and beats
func (t *TransportInfo) GetBarsBeats() (bars int, beats float64) {
	if !t.HasMusicalTime || !t.HasTimeSignature || t.TimeSigDenominator == 0 {
		return 0, 0
	}

	// Convert quarter notes to beats based on time signature
	beatsPerBar := float64(t.TimeSigNumerator)
	quarterNotesPerBeat := 4.0 / float64(t.TimeSigDenominator)
	totalBeats := t.ProjectTimeMusic / quarterNotesPerBeat

	bars = int(totalBeats / beatsPerBar)
	beats = totalBeats - float64(bars)*beatsPerBar

	return bars, beats
}

// GetSamplesPerBeat returns the number of samples per beat at current tempo
func (t *TransportInfo) GetSamplesPerBeat(sampleRate float64) float64 {
	if !t.HasTempo || t.Tempo <= 0 {
		return 0
	}

	// 60 seconds per minute / tempo = seconds per beat
	// seconds per beat * sample rate = samples per beat
	return (60.0 / t.Tempo) * sampleRate
}

// GetBeatPosition returns the current position within the current beat (0-1)
func (t *TransportInfo) GetBeatPosition() float64 {
	if !t.HasMusicalTime || !t.HasBarPosition {
		return 0
	}

	// Calculate position within current bar in quarter notes
	positionInBar := t.ProjectTimeMusic - t.BarPositionMusic

	// Convert to beat position based on time signature
	if t.HasTimeSignature && t.TimeSigDenominator > 0 {
		quarterNotesPerBeat := 4.0 / float64(t.TimeSigDenominator)
		beatPosition := (positionInBar / quarterNotesPerBeat)
		return beatPosition - float64(int(beatPosition)) // Get fractional part
	}

	// Default to quarter note = beat
	return positionInBar - float64(int(positionInBar))
}

// IsOnBeat returns true if the current position is at the start of a beat
func (t *TransportInfo) IsOnBeat(threshold float64) bool {
	beatPos := t.GetBeatPosition()
	return beatPos < threshold || beatPos > (1.0-threshold)
}
