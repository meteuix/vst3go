package dynamics

import "math"

// dbToGain is 10^(db/20), as e^(db·ln10/20): math.Exp is several times
// faster than math.Pow, and the detectors convert every sample.
func dbToGain(db float64) float64 { return math.Exp(db * (math.Ln10 / 20)) }

// gainToDB is 20·log10(g) for g > 0.
func gainToDB(g float64) float64 { return math.Log(g) * (20 / math.Ln10) }
