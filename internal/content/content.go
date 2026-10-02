// Package content is what this site does to text before it stores it: strip
// the characters nobody wrote, refuse what exists to run rather than to be
// read, and cut a long text down to a card.
//
// Almost everything stored here is written by a collective's own editors,
// signed in through the identity provider — not by strangers. That lowers the
// stakes and does not remove them: an editor pastes from a PDF, from a
// messenger, from a press release somebody else formatted, and one compromised
// editor account is all it takes for "written by staff" to stop meaning
// "written in good faith". The guards are cheap and the same for everybody.
package content

import (
	"regexp"
	"strings"
)

// Invisible characters removed on arrival.
//
// These are instructions to a renderer, not writing. Removing them changes
// nothing a person typed and nothing a reader would have seen.
//
// The dangerous ones reorder text: a line that stores one thing and displays
// another. html/template does not touch them — escaping is about markup, and
// this is not markup. On a site whose worth is that a figure quoted from a
// budget is the figure in the budget, a text that displays differently from
// what was written is worse than a script tag: the script tag is inert once
// escaped, and this is not.
var strippedRunes = map[rune]bool{
	'\u202a': true, // LEFT-TO-RIGHT EMBEDDING
	'\u202b': true, // RIGHT-TO-LEFT EMBEDDING
	'\u202c': true, // POP DIRECTIONAL FORMATTING
	'\u202d': true, // LEFT-TO-RIGHT OVERRIDE
	'\u202e': true, // RIGHT-TO-LEFT OVERRIDE
	'\u2066': true, // LEFT-TO-RIGHT ISOLATE
	'\u2067': true, // RIGHT-TO-LEFT ISOLATE
	'\u2068': true, // FIRST STRONG ISOLATE
	'\u2069': true, // POP DIRECTIONAL ISOLATE
	'\ufeff': true, // ZERO WIDTH NO-BREAK SPACE / BOM
	'\u200b': true, // ZERO WIDTH SPACE
	'\u00ad': true, // SOFT HYPHEN — invisible, and what a PDF leaves in every pasted word
}

// Deliberately NOT stripped, because in languages people here write in they
// are writing rather than trickery:
//
//	U+200C ZWNJ, U+200D ZWJ  letter-forming in Persian, Urdu, Hindi, Bengali
//	U+200E LRM, U+200F RLM   ordinary in mixed Arabic and Hebrew text, and too
//	                         weak to reorder anything on their own
//
// Stripping those would quietly corrupt an announcement written in Arabic or
// Turkish-with-Persian-names for exactly the readers it was translated for.

// Sanitise removes the invisible characters, and reports whether it had to.
//
// Deliberately the narrowest possible change: control codes out, every
// character anybody could read left alone. Line breaks and tabs are writing
// and stay.
func Sanitise(text string) (string, bool) {
	var (
		out     strings.Builder
		removed bool
	)
	out.Grow(len(text))

	for _, r := range text {
		if strippedRunes[r] {
			removed = true
			continue
		}
		out.WriteRune(r)
	}
	return out.String(), removed
}

// executablePayload matches content that exists to run in a reader's browser.
var executablePayload = regexp.MustCompile(
	`(?i)` + strings.Join([]string{
		// Elements that execute, navigate, load or can carry a handler. The
		// list errs wide: a text about a budget has no reason to contain any
		// of these, and the cost of a false positive is an editor rewording
		// one sentence.
		`<\s*(script|iframe|object|embed|svg|style|link|meta|form|base|img|` +
			`video|audio|source|input|button|body|math|details|marquee|template|frameset)\b`,
		`<\s*/\s*script\b`,

		// An event handler on anything at all. The quote is NOT required after
		// `=`: `onerror=alert(1)` is valid HTML and is what payloads actually
		// look like. A boundary before `on` is what keeps this off ordinary
		// words.
		`(^|[\s"'\x60/<])on[a-z]{3,15}\s*=`,

		// Schemes that execute or carry a document.
		`javascript\s*:`,
		`vbscript\s*:`,
		`data\s*:\s*text/html`,

		// Server-side template and lookup syntax, for the day this text is
		// rendered somewhere that is not a browser — a feed reader, a calendar
		// application, somebody's mail client.
		`\{\{.*\}\}`,
		`\$\{\s*jndi\s*:`,
	}, "|"))

// ContainsExecutablePayload reports whether a text carries content whose
// purpose is to run rather than to be read.
//
// A refusal rather than a clean-up: stripping the tag and storing the rest
// would mean whoever put it there gets their text published every time and
// loses only the payload. And the text here leaves this site — into feed
// readers, calendar applications and push notifications, none of which
// escape the way our own templates do.
//
// Escaping in the templates remains the defence that actually protects
// readers of this site. This one protects everything downstream of it, and
// neither depends on the other.
func ContainsExecutablePayload(text string) bool {
	return executablePayload.MatchString(text)
}
