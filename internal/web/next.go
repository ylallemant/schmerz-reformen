package web

import "strings"

// SafeNext keeps a "where to go afterwards" address on this site, returning
// the empty string for anything that is not a plain local path.
//
// The value arrives in a query string or a form field — which is to say from
// anybody who can write a link or a page. Without this, a sign-in or a button
// on this site would be a way to send somebody, freshly authenticated and
// trusting the address bar, to a page that merely looks like it.
//
// What is refused, and why each:
//
//   - anything not starting with "/" — an absolute URL, a scheme, a relative
//     path that depends on where it is resolved;
//   - "//host" — protocol-relative, which a browser reads as another site;
//   - "/\host" — the same thing to the browsers that treat a backslash as a
//     slash, which is most of them.
func SafeNext(next string) string {
	if !strings.HasPrefix(next, "/") ||
		strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return ""
	}
	// A control character has no business in an address, and a line break in
	// one is how a header gets split.
	for _, r := range next {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return next
}
