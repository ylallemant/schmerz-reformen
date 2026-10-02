package content

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultExcerptRunes is how much of a text a card shows before it stops.
const DefaultExcerptRunes = 260

// Excerpt cuts a text down to a card, and reports whether it cut.
//
// A feed whose entries run from one line to forty cannot be scanned, so a card
// shows the beginning and links to the whole. How it cuts:
//
//   - **Runes, never bytes.** Cutting mid-character corrupts every language
//     that is not ASCII, which here is all of them.
//   - **At a word boundary**, searching backwards from the limit, so a card
//     never ends mid-word. A single word longer than half the allowance is cut
//     hard rather than shown entire — otherwise one pasted URL would decide
//     the shape of every card on the page.
//   - **Whitespace is collapsed.** A text may have paragraphs; a card has one
//     block.
//   - **Trailing punctuation is trimmed**, so the ellipsis follows a word
//     rather than a stranded comma.
func Excerpt(text string, limit int) (excerpt string, truncated bool) {
	if limit <= 0 {
		limit = DefaultExcerptRunes
	}

	flat := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(flat) <= limit {
		return flat, false
	}

	// Walk to the limit in runes, remembering where the last word ended.
	var (
		count    int
		cut      int
		lastWord int
	)
	for offset, r := range flat {
		if count == limit {
			cut = offset
			break
		}
		if unicode.IsSpace(r) {
			lastWord = offset
		}
		count++
	}
	if cut == 0 {
		cut = len(flat)
	}

	// Back up to the word boundary, unless that would throw away most of the
	// allowance — one very long word should not empty the card.
	if lastWord > cut/2 {
		cut = lastWord
	}

	return strings.TrimRight(flat[:cut], " \t,;:-–—"), true
}
