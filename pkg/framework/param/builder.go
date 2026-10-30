package param

import (
	"fmt"
	"math"
)

// Builder provides a fluent API for creating parameters
type Builder struct {
	param        *Parameter
	defaultPlain float64
	hasDefault   bool
	center       float64
	hasCenter    bool
}

// New creates a new parameter builder
func New(id uint32, name string) *Builder {
	return &Builder{
		param: &Parameter{
			ID:           id,
			Name:         name,
			ShortName:    name,
			Min:          0,
			Max:          1,
			DefaultValue: 0,
			Flags:        CanAutomate,
		},
	}
}

// ShortName sets the short name
func (b *Builder) ShortName(name string) *Builder {
	b.param.ShortName = name
	return b
}

// Range sets the min and max values
func (b *Builder) Range(minVal, maxVal float64) *Builder {
	b.param.Min = minVal
	b.param.Max = maxVal
	return b
}

// Default sets the default value (in plain range, not normalized)
func (b *Builder) Default(value float64) *Builder {
	b.defaultPlain, b.hasDefault = value, true
	return b
}

// Linear spreads the range evenly over the normalized range (the default).
func (b *Builder) Linear() *Builder {
	b.param.Scale, b.hasCenter = ScaleLinear, false
	return b
}

// Logarithmic gives equal ratios equal travel, as frequencies and times
// are heard; the range must be positive.
func (b *Builder) Logarithmic() *Builder {
	b.param.Scale, b.hasCenter = ScaleLog, false
	return b
}

// Center skews the range so the middle of the normalized range (a knob at
// twelve o'clock) is the plain value center, for ranges that start at zero
// or cross it.
func (b *Builder) Center(center float64) *Builder {
	b.param.Scale = ScalePower
	b.center, b.hasCenter = center, true
	return b
}

// Unit sets the unit string
func (b *Builder) Unit(unit string) *Builder {
	b.param.Unit = unit
	return b
}

// Steps sets the number of discrete steps
func (b *Builder) Steps(count int32) *Builder {
	b.param.StepCount = count
	return b
}

// Flags sets parameter flags
func (b *Builder) Flags(flags uint32) *Builder {
	b.param.Flags = flags
	return b
}

// Toggle creates a boolean parameter
func (b *Builder) Toggle() *Builder {
	b.param.Min = 0
	b.param.Max = 1
	b.param.StepCount = 1
	b.param.DefaultValue = 0
	b.hasDefault = false
	return b
}

// ReadOnly marks the parameter as read-only
func (b *Builder) ReadOnly() *Builder {
	b.param.Flags |= IsReadOnly
	b.param.Flags &^= CanAutomate // Remove automation flag
	return b
}

// Hidden marks the parameter as hidden
func (b *Builder) Hidden() *Builder {
	b.param.Flags |= IsHidden
	return b
}

// Bypass marks this as the bypass parameter
func (b *Builder) Bypass() *Builder {
	b.param.Flags |= IsBypass
	return b
}

// Smooth ramps value changes linearly over seconds in
// process.Context.Smoothed (gains, mix, pan).
func (b *Builder) Smooth(seconds float64) *Builder {
	b.param.Smoothing = seconds
	b.param.SmoothingMode = SmoothLinear
	return b
}

// SmoothMultiplicative ramps value changes geometrically over seconds in
// process.Context.Smoothed (frequencies, times). The range must be positive.
func (b *Builder) SmoothMultiplicative(seconds float64) *Builder {
	b.param.Smoothing = seconds
	b.param.SmoothingMode = SmoothMultiplicative
	return b
}

// InUnit places the parameter in a unit (parameter group); see
// plugin.UnitProcessor.
func (b *Builder) InUnit(unitID int32) *Builder {
	b.param.UnitID = unitID
	return b
}

// Formatter sets custom value formatting and parsing
func (b *Builder) Formatter(format func(float64) string, parse func(string) (float64, error)) *Builder {
	b.param.formatFunc = format
	b.param.parseFunc = parse
	return b
}

// Build returns the configured parameter. It panics on a scale the range
// does not allow (a logarithmic range that is not positive, a center
// outside the range), which are programming errors.
func (b *Builder) Build() *Parameter {
	p := b.param
	if p.StepCount > 0 {
		p.Scale, p.Exponent = ScaleLinear, 0 // steps are always even
	}
	switch p.Scale {
	case ScaleLog:
		if !(p.Min > 0 && p.Max > p.Min) {
			panic(fmt.Sprintf("param %q: logarithmic range %v..%v is not positive", p.Name, p.Min, p.Max))
		}
	case ScalePower:
		if b.hasCenter {
			if !(b.center > p.Min && b.center < p.Max) {
				panic(fmt.Sprintf("param %q: center %v outside %v..%v", p.Name, b.center, p.Min, p.Max))
			}
			p.Exponent = math.Log((b.center-p.Min)/(p.Max-p.Min)) / math.Log(0.5)
		}
		if !(p.Exponent > 0) {
			panic(fmt.Sprintf("param %q: power scale exponent %v", p.Name, p.Exponent))
		}
	}
	if b.hasDefault {
		p.DefaultValue = p.Normalize(b.defaultPlain)
	}
	p.SetValue(p.DefaultValue)
	return p
}
