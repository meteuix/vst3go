// Package param provides VST3 parameter management with formatting, validation, and thread-safe value handling.
package param

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"
)

// Parameter represents a plugin parameter
type Parameter struct {
	ID           uint32
	Name         string
	ShortName    string
	Unit         string
	Min          float64
	Max          float64
	DefaultValue float64 // normalized (0-1)
	StepCount    int32
	Flags        uint32
	UnitID       int32

	// Smoothing is the ramp time in seconds that process.Context.Smoothed
	// uses when the value changes; 0 disables smoothing.
	Smoothing     float64
	SmoothingMode SmoothingMode

	// Scale spreads a continuous range over the normalized 0..1 that host
	// knobs and automation move through; Exponent is the curve of
	// ScalePower. Stepped parameters are always linear.
	Scale    Scale
	Exponent float64

	// Atomic value for lock-free access in audio thread
	value uint64 // Store as uint64 for atomic operations

	// Value formatting
	formatFunc func(float64) string
	parseFunc  func(string) (float64, error)
}

// Flags for parameters
const (
	CanAutomate     uint32 = 1 << 0
	IsReadOnly      uint32 = 1 << 1
	IsWrapAround    uint32 = 1 << 2
	IsList          uint32 = 1 << 3
	IsHidden        uint32 = 1 << 4
	IsProgramChange uint32 = 1 << 15
	IsBypass        uint32 = 1 << 16
)

// Scale is how a continuous parameter maps normalized values to plain ones.
type Scale int

const (
	// ScaleLinear spreads Min..Max evenly.
	ScaleLinear Scale = iota
	// ScaleLog gives equal ratios equal travel (frequencies, times); Min
	// must be positive.
	ScaleLog
	// ScalePower maps n to Min + (Max-Min)·n^Exponent; exponents above 1
	// give the low end more travel.
	ScalePower
)

// GetValue returns the current normalized value (0-1)
func (p *Parameter) GetValue() float64 {
	return p.GetNormalized()
}

// GetNormalized returns the current normalized value (0-1)
func (p *Parameter) GetNormalized() float64 {
	bits := atomic.LoadUint64(&p.value)
	return float64frombits(bits)
}

// SetValue sets the normalized value (0-1)
func (p *Parameter) SetValue(value float64) {
	p.SetNormalized(value)
}

// SetNormalized sets the normalized value (0-1)
func (p *Parameter) SetNormalized(value float64) {
	// Clamp to 0-1
	if value < 0 {
		value = 0
	} else if value > 1 {
		value = 1
	}

	atomic.StoreUint64(&p.value, float64bits(value))
}

// GetPlainValue converts normalized to plain value
func (p *Parameter) GetPlainValue() float64 {
	return p.GetPlain()
}

// GetPlain converts the current normalized value to a plain value.
func (p *Parameter) GetPlain() float64 {
	return p.Denormalize(p.GetNormalized())
}

// SetPlainValue converts plain to normalized value
func (p *Parameter) SetPlainValue(plain float64) {
	p.SetPlain(plain)
}

// SetPlain converts a plain value to normalized form and stores it.
func (p *Parameter) SetPlain(plain float64) {
	p.SetNormalized(p.Normalize(plain))
}

// SetFormatter sets custom value formatting
func (p *Parameter) SetFormatter(format func(float64) string, parse func(string) (float64, error)) {
	p.formatFunc = format
	p.parseFunc = parse
}

// FormatValue returns formatted parameter value
func (p *Parameter) FormatValue(normalized float64) string {
	plain := p.Denormalize(normalized)

	if p.formatFunc != nil {
		return p.formatFunc(plain)
	}

	// Default formatting
	if p.StepCount > 0 {
		// For discrete parameters, show integer
		return fmt.Sprintf("%.0f", plain)
	}
	return fmt.Sprintf("%.2f", plain)
}

// ParseValue parses string to normalized value
func (p *Parameter) ParseValue(str string) (float64, error) {
	if p.parseFunc != nil {
		plain, err := p.parseFunc(str)
		if err != nil {
			return 0, err
		}
		return p.Normalize(plain), nil
	}
	plain, err := parseLeadingNumber(str)
	if err != nil {
		return 0, err
	}
	return p.Normalize(plain), nil
}

// parseLeadingNumber parses the first number in a display string, so the
// default parser accepts formatted values such as "Q: 0.71" or "12.0 dB".
// Infinity signs map to ±Inf, which Normalize clamps to the range.
func parseLeadingNumber(str string) (float64, error) {
	str = strings.TrimSpace(str)
	if strings.Contains(str, "∞") || strings.Contains(strings.ToLower(str), "inf") {
		if strings.HasPrefix(str, "-") {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	}
	for start := 0; start < len(str); start++ {
		if strings.IndexByte("+-.0123456789", str[start]) < 0 {
			continue
		}
		end := start + 1
		for end < len(str) && strings.IndexByte("0123456789.eE+-", str[end]) >= 0 {
			end++
		}
		// Back off trailing characters that do not form a number ("1e", "5-").
		for ; end > start; end-- {
			if value, err := strconv.ParseFloat(str[start:end], 64); err == nil {
				return value, nil
			}
		}
	}
	return 0, fmt.Errorf("parse %q: no number", str)
}

// Normalize converts a plain value to normalized (0-1), clamping to the
// range.
func (p *Parameter) Normalize(plain float64) float64 {
	if p.Max <= p.Min {
		return 0
	}
	if math.IsNaN(plain) || plain <= p.Min {
		return 0
	}
	if plain >= p.Max {
		return 1
	}
	if p.StepCount > 0 {
		return (plain - p.Min) / (p.Max - p.Min)
	}
	switch p.Scale {
	case ScaleLog:
		return math.Log(plain/p.Min) / math.Log(p.Max/p.Min)
	case ScalePower:
		return math.Pow((plain-p.Min)/(p.Max-p.Min), 1/p.Exponent)
	default:
		return (plain - p.Min) / (p.Max - p.Min)
	}
}

// Denormalize converts normalized (0-1) to plain value. Stepped parameters
// snap to their steps using the VST3 rule min(steps, floor(n*(steps+1))).
func (p *Parameter) Denormalize(normalized float64) float64 {
	normalized = max(0, min(1, normalized))
	if p.StepCount > 0 {
		steps := float64(p.StepCount)
		step := min(steps, math.Floor(normalized*(steps+1)))
		return p.Min + step*(p.Max-p.Min)/steps
	}
	// Rounding must not step outside the range (a log scale at 1 can land
	// a hair above Max), as processors size buffers by it.
	switch p.Scale {
	case ScaleLog:
		if p.Min > 0 && p.Max > p.Min {
			return min(p.Max, max(p.Min, p.Min*math.Pow(p.Max/p.Min, normalized)))
		}
	case ScalePower:
		return min(p.Max, p.Min+(p.Max-p.Min)*math.Pow(normalized, p.Exponent))
	}
	return min(p.Max, p.Min+normalized*(p.Max-p.Min))
}

// Helper functions for float64 <-> uint64 conversion
func float64bits(f float64) uint64 {
	return *(*uint64)(unsafe.Pointer(&f))
}

func float64frombits(b uint64) float64 {
	return *(*float64)(unsafe.Pointer(&b))
}
