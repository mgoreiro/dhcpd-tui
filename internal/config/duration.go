package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Duration es un time.Duration que se escribe en JSON y en la TUI de forma
// legible: "6h", "1d12h", "90m", "3600" (segundos).
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

// String devuelve la forma legible ("6h", "1d2h30m", "45s").
func (d Duration) String() string {
	if d <= 0 {
		return "0"
	}
	secs := int64(time.Duration(d) / time.Second)
	var sb strings.Builder
	for _, u := range []struct {
		n int64
		s string
	}{{86400, "d"}, {3600, "h"}, {60, "m"}, {1, "s"}} {
		if secs >= u.n {
			fmt.Fprintf(&sb, "%d%s", secs/u.n, u.s)
			secs %= u.n
		}
	}
	if sb.Len() == 0 {
		return "0"
	}
	return sb.String()
}

// ParseDuration acepta "6h", "1d12h", "90m", "30s" o un número de segundos.
func ParseDuration(s string) (Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}
	var total, cur int64
	haveDigits := false
	hasUnit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			cur = cur*10 + int64(r-'0')
			haveDigits = true
			if cur > 1<<40 {
				return 0, fmt.Errorf("duración demasiado grande: %q", s)
			}
		case r == 'd' || r == 'h' || r == 'm' || r == 's':
			if !haveDigits {
				return 0, fmt.Errorf("duración no válida: %q", s)
			}
			mult := map[rune]int64{'d': 86400, 'h': 3600, 'm': 60, 's': 1}[r]
			total += cur * mult
			cur, haveDigits, hasUnit = 0, false, true
		case r == ' ':
		default:
			return 0, fmt.Errorf("duración no válida: %q (use p.ej. 6h, 90m, 1d)", s)
		}
	}
	if haveDigits {
		if hasUnit {
			return 0, fmt.Errorf("duración no válida: %q (falta unidad)", s)
		}
		total = cur // número suelto = segundos
	}
	return Duration(time.Duration(total) * time.Second), nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n int64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		*d = Duration(time.Duration(n) * time.Second)
		return nil
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
