package backend

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ylallemant/schmerz-reformen/internal/content"
	"github.com/ylallemant/schmerz-reformen/internal/geo"
)

// What an editor may write, in runes. The model's column sizes are the hard
// edge; these sit inside them so the refusal is a sentence an editor can read
// rather than a database error.
const (
	maxNameRunes    = 120
	maxTitleRunes   = 160
	maxSummaryRunes = 400
	maxBodyRunes    = 20_000
	maxPlaceRunes   = 160
	maxURLRunes     = 500
	maxSlugRunes    = 80
	maxGroupRunes   = 120
)

// cleanLine prepares a one-line field: a name, a title.
//
// Too long is refused rather than cut. Cutting is right for something a
// browser claims about itself; it is wrong for something a person wrote on
// purpose, where a title silently shortened is a title they did not write.
func cleanLine(field, value string, limit int) (string, error) {
	cleaned, _ := content.Sanitise(value)
	// A title is one line. Whatever arrived as a line break was pasted.
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	if utf8.RuneCountInString(cleaned) > limit {
		return "", huma.Error422UnprocessableEntity(
			field + " is too long: at most " + strconv.Itoa(limit) + " characters")
	}
	if content.ContainsExecutablePayload(cleaned) {
		return "", huma.Error422UnprocessableEntity(field + " contains markup that cannot be saved")
	}
	return cleaned, nil
}

// cleanText prepares a field with paragraphs in it: a description, a body.
// Line breaks are writing and stay.
func cleanText(field, value string, limit int) (string, error) {
	cleaned, _ := content.Sanitise(value)
	cleaned = strings.TrimSpace(strings.ReplaceAll(cleaned, "\r\n", "\n"))

	if utf8.RuneCountInString(cleaned) > limit {
		return "", huma.Error422UnprocessableEntity(
			field + " is too long: at most " + strconv.Itoa(limit) + " characters")
	}
	if content.ContainsExecutablePayload(cleaned) {
		return "", huma.Error422UnprocessableEntity(field + " contains markup that cannot be saved")
	}
	return cleaned, nil
}

// cleanURL accepts an http or https address, or nothing.
//
// The scheme is checked because these are rendered as links. `javascript:` in
// an href is the one place escaping does not help: the attribute is perfectly
// well-formed, and it runs.
func cleanURL(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if utf8.RuneCountInString(value) > maxURLRunes {
		return "", huma.Error422UnprocessableEntity(field + " is too long")
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", huma.Error422UnprocessableEntity(
			field + " must be a web address starting with https://")
	}
	return parsed.String(), nil
}

// cleanContact accepts what a collective publishes to be reached at: a web
// address or an email address, or nothing.
func cleanContact(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "://") {
		return cleanURL("the contact", value)
	}

	if !looksLikeEmail(value) {
		return "", huma.Error422UnprocessableEntity(
			"the contact must be an email address or a web address")
	}
	return value, nil
}

// cleanEmail accepts an email address, or nothing.
func cleanEmail(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !looksLikeEmail(value) {
		return "", huma.Error422UnprocessableEntity(field + " must be an email address")
	}
	return value, nil
}

// looksLikeEmail is deliberately shallow. Whether an address works is
// something only sending to it can say; this refuses what is plainly not one,
// and anything that could break out of the mailto: it will be rendered in.
func looksLikeEmail(value string) bool {
	at := strings.LastIndex(value, "@")
	return at >= 1 && at < len(value)-1 && !strings.ContainsAny(value, " \t\r\n<>\"'?&") &&
		utf8.RuneCountInString(value) <= 200
}

// parseBounds reads "north,south,east,west".
func parseBounds(raw string) (geo.Box, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return geo.Box{}, errors.New("bounds must be north,south,east,west")
	}

	var edges [4]float64
	for i, part := range parts {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return geo.Box{}, errors.New("bounds must be four numbers")
		}
		edges[i] = value
	}

	box := geo.Box{North: edges[0], South: edges[1], East: edges[2], West: edges[3]}
	if box.North < box.South {
		return geo.Box{}, errors.New("north is south of south")
	}
	if box.North > 90 || box.South < -90 {
		return geo.Box{}, errors.New("bounds are off the planet")
	}
	return box, nil
}

// optionalBounds reads a viewport that may be absent. Absent is everywhere.
func optionalBounds(raw string) (*geo.Box, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	box, err := parseBounds(raw)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	return &box, nil
}

// trimTo trims whitespace and caps a field at a rune count, so a long value is
// shortened rather than rejected. For text this service did not ask a person
// to write: a browser's description of itself, a line for the audit log.
func trimTo(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

// itoa renders a number for a cache key.
func itoa(n int) string { return strconv.Itoa(n) }
