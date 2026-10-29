package analysis

import "math"

// PhaseScope visualizes stereo phase relationships
type PhaseScope struct {
	bufferSize  int
	bufferL     []float64
	bufferR     []float64
	writePos    int
	count       int
	points      []PhasePoint
	decay       float64
	brightness  []float64
	maxPoints   int
	rotation    float64 // 45-degree rotation for Lissajous display
	scale       float64
	persistence float64
	out         published[scopeReading]
}

// scopeReading is the display state readers see, oldest point first.
type scopeReading struct {
	l, r       []float64
	points     []PhasePoint
	brightness []float64
	count      int
	scale      float64
}

// PhasePoint represents a point in the phase display
type PhasePoint struct {
	X, Y float64
}

// PhaseScopeMode defines the display mode
type PhaseScopeMode int

const (
	ModeLissajous  PhaseScopeMode = iota // X-Y mode (L-R)
	ModeGoniometer                       // M-S mode (rotated 45°)
	ModePolar                            // Polar display
)

// NewPhaseScope creates a new phase scope
func NewPhaseScope(bufferSize int) *PhaseScope {
	ps := &PhaseScope{
		bufferSize:  bufferSize,
		bufferL:     make([]float64, bufferSize),
		bufferR:     make([]float64, bufferSize),
		points:      make([]PhasePoint, bufferSize),
		brightness:  make([]float64, bufferSize),
		maxPoints:   bufferSize,
		rotation:    0, // Start in Lissajous mode (no rotation)
		scale:       1.0,
		decay:       0.95,
		persistence: 0.8,
	}
	ps.out.init(func(r *scopeReading) {
		r.l = make([]float64, bufferSize)
		r.r = make([]float64, bufferSize)
		r.points = make([]PhasePoint, bufferSize)
		r.brightness = make([]float64, bufferSize)
		r.scale = 1
	})
	return ps
}

// publish copies the display state, oldest point first, for readers.
func (ps *PhaseScope) publish() {
	r := ps.out.write()
	start := 0
	if ps.count == ps.bufferSize {
		start = ps.writePos
	}
	n := copy(r.l, ps.bufferL[start:ps.count])
	copy(r.l[n:], ps.bufferL[:start])
	n = copy(r.r, ps.bufferR[start:ps.count])
	copy(r.r[n:], ps.bufferR[:start])
	n = copy(r.points, ps.points[start:ps.count])
	copy(r.points[n:], ps.points[:start])
	n = copy(r.brightness, ps.brightness[start:ps.count])
	copy(r.brightness[n:], ps.brightness[:start])
	r.count, r.scale = ps.count, ps.scale
	ps.out.publish()
}

// SetMode sets the display mode
func (ps *PhaseScope) SetMode(mode PhaseScopeMode) {
	switch mode {
	case ModeLissajous:
		ps.rotation = 0
	case ModeGoniometer:
		ps.rotation = math.Pi / 4.0
	case ModePolar:
		ps.rotation = 0 // Polar mode handled differently
	}
}

// SetDecay sets the decay rate for the display (0-1)
func (ps *PhaseScope) SetDecay(decay float64) {
	if decay >= 0 && decay <= 1 {
		ps.decay = decay
	}
}

// SetPersistence sets the trail persistence (0-1)
func (ps *PhaseScope) SetPersistence(persistence float64) {
	if persistence >= 0 && persistence <= 1 {
		ps.persistence = persistence
	}
}

// SetScale sets the display scale factor
func (ps *PhaseScope) SetScale(scale float64) {
	if scale > 0 {
		ps.scale = scale
	}
}

// Process updates the phase scope with stereo samples
func (ps *PhaseScope) Process(samplesL, samplesR []float64) {
	if len(samplesL) != len(samplesR) {
		return
	}

	// Decay existing brightness values
	for i := range ps.brightness {
		ps.brightness[i] *= ps.decay
	}

	// Add new samples
	for i := 0; i < len(samplesL) && i < len(samplesR); i++ {
		ps.bufferL[ps.writePos] = samplesL[i]
		ps.bufferR[ps.writePos] = samplesR[i]

		// Calculate phase point
		l := samplesL[i] * ps.scale
		r := samplesR[i] * ps.scale

		// Apply rotation if in goniometer mode
		if ps.rotation != 0 {
			// Rotate coordinates for goniometer display
			// This converts L-R to M-S representation
			cos := math.Cos(ps.rotation)
			sin := math.Sin(ps.rotation)
			x := l*cos - r*sin
			y := l*sin + r*cos
			ps.points[ps.writePos] = PhasePoint{X: x, Y: y}
		} else {
			// Lissajous mode: X=L, Y=R
			ps.points[ps.writePos] = PhasePoint{X: l, Y: r}
		}

		// Set brightness to maximum for new points
		ps.brightness[ps.writePos] = 1.0

		ps.writePos = (ps.writePos + 1) % ps.bufferSize
		if ps.count < ps.bufferSize {
			ps.count++
		}
	}
	ps.publish()
}

// GetPoints returns the current display points with brightness, oldest
// first.
func (ps *PhaseScope) GetPoints() ([]PhasePoint, []float64) {
	var points []PhasePoint
	var brightness []float64
	ps.out.read(func(r *scopeReading) {
		points = append([]PhasePoint{}, r.points[:r.count]...)
		brightness = append([]float64{}, r.brightness[:r.count]...)
	})
	return points, brightness
}

// GetPolarData returns data formatted for polar display
func (ps *PhaseScope) GetPolarData() ([]float64, []float64, []float64) {
	var radius, angle, bright []float64
	ps.out.read(func(r *scopeReading) {
		radius = make([]float64, r.count)
		angle = make([]float64, r.count)
		bright = append([]float64{}, r.brightness[:r.count]...)
		for i := range r.count {
			x := r.l[i] * r.scale
			y := r.r[i] * r.scale
			radius[i] = math.Sqrt(x*x + y*y)
			angle[i] = math.Atan2(y, x)
		}
	})
	return radius, angle, bright
}

// GetStatistics returns phase statistics
func (ps *PhaseScope) GetStatistics() PhaseScopeStats {
	var stats PhaseScopeStats
	ps.out.read(func(r *scopeReading) { stats = scopeStatistics(r.l[:r.count], r.r[:r.count]) })
	return stats
}

// scopeStatistics computes the statistics of the displayed samples.
func scopeStatistics(left, right []float64) PhaseScopeStats {
	stats := PhaseScopeStats{}
	if len(left) == 0 {
		return stats
	}

	// Average width and balance
	sumM := 0.0
	sumS := 0.0
	maxRadius := 0.0
	for i, l := range left {
		r := right[i]
		sumM += math.Abs((l + r) * 0.5)
		sumS += math.Abs((l - r) * 0.5)
		maxRadius = math.Max(maxRadius, math.Sqrt(l*l+r*r))
	}
	stats.AverageMid = sumM / float64(len(left))
	stats.AverageSide = sumS / float64(len(left))
	stats.MaxRadius = maxRadius

	// Width (side to mid ratio)
	if stats.AverageMid > 0 {
		stats.Width = stats.AverageSide / stats.AverageMid
	}

	// Phase concentration: from the standard deviation of the angles
	sumAngle := 0.0
	sumAngleSq := 0.0
	validPoints := 0
	for i, l := range left {
		r := right[i]
		if l != 0 || r != 0 {
			angle := math.Atan2(r, l)
			sumAngle += angle
			sumAngleSq += angle * angle
			validPoints++
		}
	}
	if validPoints > 0 {
		meanAngle := sumAngle / float64(validPoints)
		variance := sumAngleSq/float64(validPoints) - meanAngle*meanAngle
		stats.PhaseConcentration = 1.0 / (1.0 + math.Sqrt(math.Abs(variance)))
		stats.DominantAngle = meanAngle
	}
	return stats
}

// PhaseScopeStats contains statistical information about the phase display
type PhaseScopeStats struct {
	AverageMid         float64
	AverageSide        float64
	Width              float64
	MaxRadius          float64
	PhaseConcentration float64 // 0-1, higher means more concentrated
	DominantAngle      float64 // Dominant phase angle in radians
}

// Reset clears the phase scope display
func (ps *PhaseScope) Reset() {
	// Clear buffers
	for i := range ps.bufferL {
		ps.bufferL[i] = 0
		ps.bufferR[i] = 0
		ps.brightness[i] = 0
	}

	// Reset points
	for i := range ps.points {
		ps.points[i] = PhasePoint{X: 0, Y: 0}
	}

	// Reset counters
	ps.writePos = 0
	ps.count = 0
	ps.publish()
}

// VectorScope provides a vector scope display with graticule
type VectorScope struct {
	phaseScope *PhaseScope
	grid       []PhasePoint
	labels     []VectorScopeLabel
}

// VectorScopeLabel represents a label on the vector scope
type VectorScopeLabel struct {
	Position PhasePoint
	Text     string
}

// NewVectorScope creates a new vector scope
func NewVectorScope(bufferSize int) *VectorScope {
	vs := &VectorScope{
		phaseScope: NewPhaseScope(bufferSize),
	}

	// Set up for goniometer mode
	vs.phaseScope.SetMode(ModeGoniometer)

	// Generate graticule
	vs.generateGraticule()

	return vs
}

// generateGraticule creates the vector scope grid and labels
func (vs *VectorScope) generateGraticule() {
	// Create circular grid lines
	numCircles := 5
	numRadialLines := 12

	vs.grid = make([]PhasePoint, 0)

	// Concentric circles
	for c := 1; c <= numCircles; c++ {
		radius := float64(c) / float64(numCircles)
		numPoints := 64

		for i := 0; i < numPoints; i++ {
			angle := 2.0 * math.Pi * float64(i) / float64(numPoints)
			x := radius * math.Cos(angle)
			y := radius * math.Sin(angle)
			vs.grid = append(vs.grid, PhasePoint{X: x, Y: y})
		}
	}

	// Radial lines
	for i := 0; i < numRadialLines; i++ {
		angle := 2.0 * math.Pi * float64(i) / float64(numRadialLines)
		x := math.Cos(angle)
		y := math.Sin(angle)

		// Line from center to edge
		vs.grid = append(vs.grid, PhasePoint{X: 0, Y: 0})
		vs.grid = append(vs.grid, PhasePoint{X: x, Y: y})
	}

	// Labels for key positions
	vs.labels = []VectorScopeLabel{
		{Position: PhasePoint{X: 0, Y: 1}, Text: "M"},  // Mono
		{Position: PhasePoint{X: -1, Y: 0}, Text: "L"}, // Left
		{Position: PhasePoint{X: 1, Y: 0}, Text: "R"},  // Right
		{Position: PhasePoint{X: 0, Y: -1}, Text: "S"}, // Side
	}
}

// Process updates the vector scope
func (vs *VectorScope) Process(samplesL, samplesR []float64) {
	vs.phaseScope.Process(samplesL, samplesR)
}

// GetDisplay returns points, brightness, grid, and labels
func (vs *VectorScope) GetDisplay() (points []PhasePoint, brightness []float64, grid []PhasePoint, labels []VectorScopeLabel) {
	points, brightness = vs.phaseScope.GetPoints()
	return points, brightness, vs.grid, vs.labels
}

// GetStatistics returns phase statistics
func (vs *VectorScope) GetStatistics() PhaseScopeStats {
	return vs.phaseScope.GetStatistics()
}

// Reset clears the display
func (vs *VectorScope) Reset() {
	vs.phaseScope.Reset()
}
