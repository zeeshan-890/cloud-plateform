package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Spec is a parsed schedule expression. Supported forms:
//
//	@hourly @daily @weekly @monthly @yearly
//	@every <duration>        (1m .. 744h)
//	m h dom mon dow          (standard 5-field cron: *, lists, ranges, steps)
type Spec struct {
	every            time.Duration
	min              uint64 // bitsets
	hour             uint64
	dom              uint64
	mon              uint64
	dow              uint64
	domStar, dowStar bool
}

const (
	minEvery = time.Minute
	maxEvery = 744 * time.Hour
)

// Parse validates and compiles an expression.
func Parse(expr string) (*Spec, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty schedule")
	}
	if len(expr) > 100 {
		return nil, fmt.Errorf("schedule too long")
	}
	low := strings.ToLower(expr)
	switch low {
	case "@hourly":
		return Parse("0 * * * *")
	case "@daily", "@midnight":
		return Parse("0 0 * * *")
	case "@weekly":
		return Parse("0 0 * * 0")
	case "@monthly":
		return Parse("0 0 1 * *")
	case "@yearly", "@annually":
		return Parse("0 0 1 1 *")
	}
	if strings.HasPrefix(low, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(low[len("@every "):]))
		if err != nil {
			return nil, fmt.Errorf("invalid @every duration")
		}
		if d < minEvery || d > maxEvery {
			return nil, fmt.Errorf("@every must be between %s and %s", minEvery, maxEvery)
		}
		return &Spec{every: d}, nil
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("expected 5 cron fields (minute hour day month weekday), got %d", len(f))
	}
	s := &Spec{}
	var err error
	if s.min, _, err = field(f[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if s.hour, _, err = field(f[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if s.dom, s.domStar, err = field(f[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if s.mon, _, err = field(f[3], 1, 12); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if s.dow, s.dowStar, err = field(f[4], 0, 7); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	if s.dow&(1<<7) != 0 { // 7 == Sunday
		s.dow |= 1
	}
	return s, nil
}

func field(spec string, lo, hi int) (bits uint64, star bool, err error) {
	for _, part := range strings.Split(spec, ",") {
		step := 1
		rng := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			rng = part[:i]
			step, err = strconv.Atoi(part[i+1:])
			if err != nil || step < 1 {
				return 0, false, fmt.Errorf("bad step in %q", part)
			}
		}
		a, b := lo, hi
		switch {
		case rng == "*":
			if step == 1 && len(strings.Split(spec, ",")) == 1 {
				star = true
			}
		case strings.Contains(rng, "-"):
			p := strings.SplitN(rng, "-", 2)
			if a, err = strconv.Atoi(p[0]); err != nil {
				return 0, false, fmt.Errorf("bad value %q", p[0])
			}
			if b, err = strconv.Atoi(p[1]); err != nil {
				return 0, false, fmt.Errorf("bad value %q", p[1])
			}
		default:
			if a, err = strconv.Atoi(rng); err != nil {
				return 0, false, fmt.Errorf("bad value %q", rng)
			}
			b = a
			if strings.Contains(part, "/") {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return 0, false, fmt.Errorf("%q out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, star, nil
}

func (s *Spec) dayMatches(t time.Time) bool {
	if s.mon&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := s.dom&(1<<uint(t.Day())) != 0
	dowOK := s.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dowOK
	case s.dowStar:
		return domOK
	default:
		return domOK || dowOK // standard cron: either restriction matches
	}
}

// Next returns the first activation strictly after from (UTC). The zero time means
// no activation within five years.
func (s *Spec) Next(from time.Time) time.Time {
	from = from.UTC()
	if s.every > 0 {
		return from.Add(s.every)
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if s.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if s.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// NextRun parses expr and returns the next activation after from (zero when invalid).
func NextRun(expr string, from time.Time) time.Time {
	s, err := Parse(expr)
	if err != nil {
		return time.Time{}
	}
	return s.Next(from)
}
