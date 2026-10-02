package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ContrastRatio returns the WCAG contrast ratio between two hex colours, from
// 1 to 21, or 0 when either value is not a plain hex — a var() or an rgba() is
// skipped rather than guessed at.
func ContrastRatio(a, b string) float64 {
	la, okA := relativeLuminance(a)
	lb, okB := relativeLuminance(b)
	if !okA || !okB {
		return 0
	}

	high, low := la, lb
	if low > high {
		high, low = low, high
	}
	return (high + 0.05) / (low + 0.05)
}

func relativeLuminance(hex string) (float64, bool) {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return 0, false
	}

	linear := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b), true
}

func parseHex(s string) (r, g, b float64, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 { // #abc is #aabbcc
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, false
	}

	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

// ContrastWarning is one pairing that fails to meet its minimum.
type ContrastWarning struct {
	// Mode is "light" or "dark".
	Mode string `json:"mode"`
	// Pair reads as "text on bg".
	Pair  string  `json:"pair"`
	Ratio float64 `json:"ratio"`
	Min   float64 `json:"min"`
}

// contrastPairs are the readability-critical pairings and their WCAG AA
// minimums: 4.5 for body text, 3.0 for large text and UI accents.
var contrastPairs = []struct {
	foreground, background string
	min                    float64
}{
	{"text", "bg", 4.5},
	{"text", "surface", 4.5},
	// What sits on a filled button, and on the masthead. These are the pairs
	// a designer changing one colour is most likely to break without seeing
	// it: the button still looks like a button.
	{"on-accent", "accent", 4.5},
	{"on-band", "band", 4.5},
	{"on-mark", "mark-topic", 3.0},
	{"on-mark", "mark-action", 3.0},
	{"on-mark", "mark-collective", 3.0},
	{"text-muted", "bg", 3.0},
	{"error", "bg", 3.0},
	{"warn", "bg", 3.0},
	{"good", "bg", 3.0},
}

// CheckContrast reports readability problems across both modes.
//
// It is advisory and never blocks a theme from being saved or selected: an
// operator who wants an unusual palette gets a warning, not a refusal. A token
// that is not a plain hex is skipped.
func CheckContrast(tokens []Token) []ContrastWarning {
	light := map[string]string{}
	dark := map[string]string{}
	for _, t := range tokens {
		light[t.Name] = t.Light
		dark[t.Name] = t.Dark
	}

	var out []ContrastWarning
	for _, mode := range []struct {
		name   string
		values map[string]string
	}{{"light", light}, {"dark", dark}} {
		for _, pair := range contrastPairs {
			ratio := ContrastRatio(mode.values[pair.foreground], mode.values[pair.background])
			if ratio > 0 && ratio < pair.min {
				out = append(out, ContrastWarning{
					Mode:  mode.name,
					Pair:  fmt.Sprintf("%s on %s", pair.foreground, pair.background),
					Ratio: math.Round(ratio*100) / 100,
					Min:   pair.min,
				})
			}
		}
	}
	return out
}
