package web

import "testing"

// TestAfterAnythingYouStayOnThisSite: `next` arrives in a link anybody can
// write, and somebody who has just signed in trusts the address bar.
func TestAfterAnythingYouStayOnThisSite(t *testing.T) {
	for next, want := range map[string]string{
		"/topics/abc":              "/topics/abc",
		"/calendar?month=2026-10":  "/calendar?month=2026-10",
		"/collectives/duesseldorf": "/collectives/duesseldorf",
		"/":                        "/",

		"":                           "",
		"topics":                     "",
		"https://evil.example/":      "",
		"//evil.example/":            "",
		`/\evil.example`:             "",
		"javascript:alert(1)":        "",
		"/topics\r\nSet-Cookie: x=1": "",
	} {
		if got := SafeNext(next); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", next, got, want)
		}
	}
}
