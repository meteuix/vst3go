package ara

import "sort"

// Content returns the region's content in playback time: the content of
// its modification inside the played range, moved (and with Timestretch
// scaled) to where the region plays. Model thread.
func (r *PlaybackRegion) Content() *Content {
	c := r.modification.Content()
	if c == nil {
		return nil
	}
	p := r.props
	ratio := 1.0
	if p.Flags&Timestretch != 0 && p.DurationInModification > 0 {
		ratio = p.DurationInPlayback / p.DurationInModification
	}
	toPlayback := func(t float64) float64 { return p.StartInPlayback + (t-p.StartInModification)*ratio }
	in := c.Range(TimeRange{Start: p.StartInModification, Duration: p.DurationInModification})
	out := &Content{Grade: c.Grade, Bars: in.Bars}
	if in.Notes != nil {
		out.Notes = make([]Note, len(in.Notes))
		for i, n := range in.Notes {
			n.Start = toPlayback(n.Start)
			n.Attack *= ratio
			n.Duration *= ratio
			n.SignalDuration *= ratio
			out.Notes[i] = n
		}
	}
	if in.Tempo != nil {
		out.Tempo = make([]TempoEntry, len(in.Tempo))
		for i, e := range in.Tempo {
			out.Tempo[i] = TempoEntry{Time: toPlayback(e.Time), Quarter: e.Quarter}
		}
	}
	return out
}

// Range returns the content that intersects r: the notes sounding in it,
// the tempo entries in it with the last one before and the first one after
// it (so the tempo map covers the range), and the bar signatures in effect
// in it (all of them when the content has no tempo map to place them).
func (c *Content) Range(r TimeRange) *Content {
	end := r.Start + r.Duration
	out := &Content{Grade: c.Grade}
	if c.Notes != nil {
		out.Notes = []Note{}
		for _, n := range c.Notes {
			if n.Start < end && n.Start+max(n.Duration, n.SignalDuration) > r.Start {
				out.Notes = append(out.Notes, n)
			}
		}
	}
	if c.Tempo != nil {
		lo := sort.Search(len(c.Tempo), func(i int) bool { return c.Tempo[i].Time > r.Start })
		hi := sort.Search(len(c.Tempo), func(i int) bool { return c.Tempo[i].Time >= end })
		out.Tempo = c.Tempo[max(lo-1, 0):min(hi+1, len(c.Tempo))]
	}
	if c.Bars != nil {
		out.Bars = c.Bars
		if len(c.Tempo) >= 2 {
			q0, q1 := quarterAt(c.Tempo, r.Start), quarterAt(c.Tempo, end)
			lo := sort.Search(len(c.Bars), func(i int) bool { return c.Bars[i].Quarter > q0 })
			hi := sort.Search(len(c.Bars), func(i int) bool { return c.Bars[i].Quarter >= q1 })
			out.Bars = c.Bars[max(lo-1, 0):max(hi, lo)]
		}
	}
	return out
}

// quarterAt maps a time to quarter notes with a tempo map of at least two
// entries, extending the first and last tempo beyond the map.
func quarterAt(tempo []TempoEntry, t float64) float64 {
	i := sort.Search(len(tempo), func(i int) bool { return tempo[i].Time > t })
	i = min(max(i, 1), len(tempo)-1)
	a, b := tempo[i-1], tempo[i]
	return a.Quarter + (t-a.Time)*(b.Quarter-a.Quarter)/(b.Time-a.Time)
}
