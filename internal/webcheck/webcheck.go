// Package webcheck holds the checks both web services run against their own
// templates, scripts and catalogues.
//
// It is test support that lives outside a _test.go file for one reason: the
// console and the frontend need exactly the same checks, and a copy in each
// would be two copies that drift — the one place a check must not, since its
// whole job is to notice drift somewhere else.
//
// Every function takes the service's embedded assets, so what is checked is
// what ships in the binary rather than what happens to be on disk.
package webcheck

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// SourceLanguage is the language the others are measured against.
//
// English, although German is what most people here read: it is the shortest
// of the languages a catalogue is likely to gain, so a length budget counted
// from it is the one that catches a label outgrowing its button.
const SourceLanguage = "en"

// The length budget. A short string is a label on a control and gets more
// room in proportion, because "Edit" becoming "Bearbeiten" is unremarkable; a
// long one is prose and should stay roughly as long as what it translates.
const (
	uiStringLimit  = 20
	uiBudgetFactor = 1.6
	uiBudgetSlack  = 10
	proseBudget    = 1.45
)

// Catalogues reads the catalogues under dir in a service's assets, keyed by
// language, each merged over the shared catalogue for that language.
//
// Merged, because that is how the service loads them: a key defined in the
// shared file is defined for the service too, and a check that did not know
// that would report every date format as missing.
func Catalogues(t testing.TB, assets fs.FS, dir string) map[string]map[string]string {
	t.Helper()

	shared := readCatalogues(t, web.SharedCatalogues(), "locales")
	own := readCatalogues(t, assets, dir)
	if len(own) == 0 {
		t.Fatalf("no catalogues found in %s, so this test proves nothing", dir)
	}

	merged := make(map[string]map[string]string, len(own))
	for lang, messages := range own {
		merged[lang] = map[string]string{}
		for key, value := range shared[lang] {
			merged[lang][key] = value
		}
		for key, value := range messages {
			merged[lang][key] = value
		}
	}
	return merged
}

func readCatalogues(t testing.TB, fsys fs.FS, dir string) map[string]map[string]string {
	t.Helper()

	entries, err := fs.Glob(fsys, path.Join(dir, "*.toml"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}

	out := make(map[string]map[string]string, len(entries))
	for _, entry := range entries {
		raw, err := fs.ReadFile(fsys, entry)
		if err != nil {
			t.Fatalf("read %s: %v", entry, err)
		}
		messages := map[string]string{}
		if err := toml.Unmarshal(raw, &messages); err != nil {
			t.Fatalf("parse %s: %v", entry, err)
		}
		parts := strings.Split(strings.TrimSuffix(path.Base(entry), ".toml"), ".")
		out[parts[len(parts)-1]] = messages
	}
	return out
}

// EveryCatalogueHasEveryKey checks that no language is missing a key another
// has. A missing one renders as the key itself, on a page only readers of
// that language ever see.
func EveryCatalogueHasEveryKey(t *testing.T, assets fs.FS, dir string) {
	t.Helper()

	all := Catalogues(t, assets, dir)
	source, ok := all[SourceLanguage]
	if !ok {
		t.Fatalf("there is no %s catalogue to measure the others against", SourceLanguage)
	}

	for lang, messages := range all {
		if lang == SourceLanguage {
			continue
		}
		for _, key := range sortedKeys(source) {
			if _, ok := messages[key]; !ok {
				t.Errorf("%s is missing %q", lang, key)
			}
		}
		for _, key := range sortedKeys(messages) {
			if _, ok := source[key]; !ok {
				t.Errorf("%s has %q, which %s does not", lang, key, SourceLanguage)
			}
		}
	}
}

// TranslationsRespectTheLengthBudget checks that a translation stays close
// enough to the source length that the layout survives it.
//
// Format patterns are exempt: "{{.Weekday}}, {{.Day}}. {{.Month}} {{.Year}}"
// is not text anybody reads at that length, and what it expands to is decided
// by the names it is filled with.
func TranslationsRespectTheLengthBudget(t *testing.T, assets fs.FS, dir string) {
	t.Helper()

	all := Catalogues(t, assets, dir)
	source := all[SourceLanguage]

	for lang, messages := range all {
		if lang == SourceLanguage {
			continue
		}
		for _, key := range sortedKeys(messages) {
			original, ok := source[key]
			if !ok || strings.HasPrefix(key, "format.") {
				continue
			}
			translated := messages[key]

			length := len([]rune(original))
			limit := int(float64(length) * proseBudget)
			if length <= uiStringLimit {
				limit = max(int(float64(length)*uiBudgetFactor), length+uiBudgetSlack)
			}

			if got := len([]rune(translated)); got > limit {
				t.Errorf("%s: %q is %d characters, budget is %d\n  %s: %s\n  %s: %s",
					lang, key, got, limit, SourceLanguage, original, lang, translated)
			}
		}
	}
}

// How a key is asked for.
//
// A template asks with `.T` or `.Tf`; a partial is handed one as an argument
// called "Key" or "Hint"; a handler asks in Go, either directly or by naming
// the page's title. The scripts never ask: every string they use is rendered
// into the page by a template, so scanning the templates covers them too.
var keyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\.Tf?[( ]"([a-z_]+(?:\.[a-z_0-9]+)+)"`),
	regexp.MustCompile(`"(?:Key|Hint)" +"([a-z_]+(?:\.[a-z_0-9]+)+)"`),
	regexp.MustCompile(`(?:newPage|Data)\([^"\n]*"([a-z_]+(?:\.[a-z_0-9]+)+)"`),
	regexp.MustCompile(`:\s+"((?:notice|error)\.[a-z_0-9]+)"`),
}

// prefixPattern finds keys assembled at render time: `printf "kind.%s" .Kind`.
// The suffix is not knowable here, so what is checked is that the family
// exists at all — and Enumerated is how a caller names the members.
var prefixPattern = regexp.MustCompile(`printf "([a-z_]+(?:\.[a-z_]+)*)\.%[sd]"`)

// EveryKeyAskedForIsDefined checks the catalogue against the templates and
// the handlers, which is the drift that actually happens.
//
// A missing key renders as the key itself — no error, no log — so the only
// symptom is a column heading reading `field.starts_at` on a page somebody
// has to visit to notice.
//
// enumerated are keys built at render time from a value, which no scan of the
// source can find: the caller lists them, usually by looping over the same
// slice the form offers.
func EveryKeyAskedForIsDefined(t *testing.T, assets fs.FS, dir, templateGlob string, enumerated []string) {
	t.Helper()

	defined := Catalogues(t, assets, dir)[SourceLanguage]

	templates, err := fs.Glob(assets, templateGlob)
	if err != nil {
		t.Fatalf("glob templates: %v", err)
	}
	if len(templates) == 0 {
		t.Fatal("no templates were scanned, so this test proves nothing")
	}
	handlers, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob handlers: %v", err)
	}

	asked := map[string][]string{}
	prefixes := map[string][]string{}
	collect := func(name, body string) {
		for _, pattern := range keyPatterns {
			for _, match := range pattern.FindAllStringSubmatch(body, -1) {
				asked[match[1]] = append(asked[match[1]], name)
			}
		}
		for _, match := range prefixPattern.FindAllStringSubmatch(body, -1) {
			prefixes[match[1]] = append(prefixes[match[1]], name)
		}
	}

	for _, name := range templates {
		raw, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		collect(name, string(raw))
	}
	for _, name := range handlers {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		collect(name, string(raw))
	}
	if len(asked) == 0 {
		t.Fatal("no keys were found at all, so this test proves nothing")
	}

	for _, key := range sortedKeys(asked) {
		if _, ok := defined[key]; !ok {
			t.Errorf("%q is asked for in %s and is not defined", key, strings.Join(asked[key], ", "))
		}
	}

	for _, prefix := range sortedKeys(prefixes) {
		var members int
		for key := range defined {
			if strings.HasPrefix(key, prefix+".") {
				members++
			}
		}
		if members == 0 {
			t.Errorf("keys under %q are built in %s and none is defined",
				prefix+".", strings.Join(prefixes[prefix], ", "))
		}
	}

	for _, key := range enumerated {
		if _, ok := defined[key]; !ok {
			t.Errorf("%q is built at render time from a value the forms offer, and is not defined", key)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// EveryInlineScriptCarriesTheNonce checks the templates against the
// Content-Security-Policy.
//
// Under the policy an inline `<script>` without the nonce is refused by the
// browser, silently and by design. So the failure mode of forgetting one is a
// page whose behaviour quietly stops — the theme flashing the wrong way, a
// table never filling — with nothing in any log and nothing to see except in
// a browser console somebody has to think to open.
//
// The templates are scanned rather than the rendered pages, because a page
// that is never rendered by a test would otherwise never be checked.
func EveryInlineScriptCarriesTheNonce(t *testing.T, assets fs.FS, templateGlob string) {
	t.Helper()

	inline := regexp.MustCompile(`<script(?:\s[^>]*)?>`)
	hasSrc := regexp.MustCompile(`\ssrc=`)
	hasNonce := regexp.MustCompile(`\snonce=`)

	var found int
	for name, body := range readAll(t, assets, templateGlob) {
		for _, tag := range inline.FindAllString(body, -1) {
			if hasSrc.MatchString(tag) {
				continue
			}
			found++
			if !hasNonce.MatchString(tag) {
				t.Errorf("%s has an inline script with no nonce: %s", name, tag)
			}
		}
	}
	if found == 0 {
		t.Fatal("no inline scripts were found, so this test proves nothing")
	}
}

// NoPageLoadsForeignResources checks that no template loads anything from
// another origin.
//
// A resource loaded from somewhere else hands that somewhere the address of
// everybody reading the page, and a script from there runs on the origin
// where a session cookie lives. The policy enforces this in the browser; the
// test exists because a policy is only as good as nobody having quietly added
// an exception to it, and because a blocked resource fails silently by
// design.
//
// A plain link a reader chooses to follow is not a resource the page loads,
// so `<a href>` to elsewhere is not what this looks for: it is `src=`, and
// `href=` on the elements that fetch.
func NoPageLoadsForeignResources(t *testing.T, assets fs.FS, templateGlob string) {
	t.Helper()

	// Anything absolute that the browser fetches by itself: a script, an
	// image, a frame, a stylesheet or another <link>.
	loads := regexp.MustCompile(
		`<(?:script|img|iframe|audio|video|source|embed|object|link)\b[^>]*\s(?:src|href|data)="(?:https?:)?//[^"]+"`)

	for name, body := range readAll(t, assets, templateGlob) {
		for _, match := range loads.FindAllString(body, -1) {
			t.Errorf("%s loads something from another origin: %s", name, match)
		}
	}
}

// NoInlineHandlersOrStyles checks that no template carries an `on…=`
// attribute or a `style=` attribute.
//
// The policy refuses both, so either is markup that silently does nothing.
// And both are how a template grows a second place where behaviour or colour
// is decided, away from the scripts and the tokens that are checked.
func NoInlineHandlersOrStyles(t *testing.T, assets fs.FS, templateGlob string) {
	t.Helper()

	inline := regexp.MustCompile(`<[a-zA-Z][^>]*\s(on[a-z]+|style)\s*=`)

	for name, body := range readAll(t, assets, templateGlob) {
		// Template comments describe attributes they are not; take them out
		// before looking.
		body = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`).ReplaceAllString(body, "")
		for _, match := range inline.FindAllStringSubmatch(body, -1) {
			t.Errorf("%s has an inline %q attribute, which the Content-Security-Policy refuses", name, match[1])
		}
	}
}

// ScriptsNeverBuildMarkupFromData checks the service's own scripts.
//
// Everything a script puts on a page here came from somewhere outside it — a
// collective's name, a place, a title — and a script that assembled HTML from
// that would be the one path around `html/template`'s escaping. So the rule
// is that none does: elements are made with createElement and filled with
// textContent.
//
// The rule has no exception, not even for `innerHTML = ""`, which is
// harmless. A rule with a carve-out invites the next person to decide their
// case also qualifies; textContent clears an element just as well.
//
// Only the service's own scripts are walked. The vendored map library uses
// innerHTML for its own controls, and what matters there is whether *our*
// code ever hands it a string built from data — which this is what checks.
func ScriptsNeverBuildMarkupFromData(t *testing.T, assets fs.FS, dir string) {
	t.Helper()

	dangerous := regexp.MustCompile(
		`\.innerHTML\s*=|\.outerHTML\s*=|insertAdjacentHTML|document\.write|` +
			`\.html\s*\(|new Function|eval\s*\(|bindPopup\s*\(\s*["'\x60]|setContent\s*\(\s*["'\x60]`)

	var checked int
	err := fs.WalkDir(assets, dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".js") {
			return err
		}
		checked++

		source, err := fs.ReadFile(assets, name)
		if err != nil {
			return err
		}
		for _, match := range dangerous.FindAllString(string(source), -1) {
			t.Errorf("%s builds markup from data with %q", name, strings.TrimSpace(match))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	// A guard that silently checked nothing would be worse than no guard.
	if checked == 0 {
		t.Fatal("no scripts were checked; the walk found nothing")
	}
}

func readAll(t testing.TB, assets fs.FS, glob string) map[string]string {
	t.Helper()

	names, err := fs.Glob(assets, glob)
	if err != nil {
		t.Fatalf("glob %s: %v", glob, err)
	}
	if len(names) == 0 {
		t.Fatalf("nothing matched %s, so this test proves nothing", glob)
	}

	out := make(map[string]string, len(names))
	for _, name := range names {
		raw, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(raw)
	}
	return out
}
