// Package web is the shared plumbing for the two server-rendered services:
// translation, template rendering and embedded static assets.
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// LanguageCookie carries a visitor's explicit choice to the server.
//
// **It is a copy, not the record.** The choice lives in the browser's
// localStorage (see internal/theme/language.js), and the cookie exists only
// because the server renders the page and never sees localStorage. The script
// keeps the two in step: it writes the cookie whenever the choice changes, and
// clears it when there is no choice stored — so a browser with no choice gets
// its own language settings, whatever cookie it was carrying.
//
// Not HttpOnly, for that reason: the script has to be able to write and clear
// it. It holds two letters, and a page that can read it learns nothing it
// could not read from the page's own lang attribute.
//
// A different name from the HttpOnly cookie an earlier version set, because a
// script cannot overwrite an HttpOnly cookie of the same name.
const LanguageCookie = "schmerz_language"

// Localization holds the loaded message catalogues.
//
// Translations live in TOML files outside the code, so they can be exported,
// handed to a translator and imported back without anyone touching Go.
type Localization struct {
	bundle    *i18n.Bundle
	supported []string
	fallback  string
}

// NewLocalization loads every catalogue under dir in fsys. Files are named
// active.<lang>.toml, which is what `goi18n merge` produces.
func NewLocalization(fsys fs.FS, dir, fallback string) (*Localization, error) {
	tag, err := language.Parse(fallback)
	if err != nil {
		return nil, fmt.Errorf("invalid fallback language %q: %w", fallback, err)
	}

	bundle := i18n.NewBundle(tag)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)

	// The shared catalogue first, so a service's own may override a key from
	// it. It carries what both services have to say identically: how a date
	// and a sum of money are written, and what the model's enumerations are
	// called. See locales/shared.*.toml.
	if err := loadShared(bundle); err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read locales in %s: %w", dir, err)
	}

	var supported []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".toml") {
			continue
		}
		if _, err := bundle.LoadMessageFileFS(fsys, path.Join(dir, name)); err != nil {
			return nil, fmt.Errorf("load %s: %w", name, err)
		}
		if lang := languageFromFilename(name); lang != "" {
			supported = append(supported, lang)
		}
	}
	if len(supported) == 0 {
		return nil, fmt.Errorf("no translation catalogues found in %s", dir)
	}

	return &Localization{bundle: bundle, supported: supported, fallback: fallback}, nil
}

// shared holds the catalogue both web services load.
//
//go:embed locales/*.toml
var shared embed.FS

// loadShared adds the shared catalogue to a bundle.
//
// It does not add to the languages a service offers: a language is offered
// when the service's own catalogue has it, and a shared file for a language
// the service was never translated into would otherwise put a button in the
// masthead that leads to a page of keys.
func loadShared(bundle *i18n.Bundle) error {
	entries, err := fs.ReadDir(shared, "locales")
	if err != nil {
		return fmt.Errorf("read the shared locales: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		if _, err := bundle.LoadMessageFileFS(shared, path.Join("locales", entry.Name())); err != nil {
			return fmt.Errorf("load shared %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// SharedCatalogues exposes the shared catalogue files, for the tests that
// check a key used by a template exists somewhere.
func SharedCatalogues() fs.FS { return shared }

// languageFromFilename extracts "fr" from "active.fr.toml".
func languageFromFilename(name string) string {
	parts := strings.Split(strings.TrimSuffix(name, ".toml"), ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// Supported lists the languages the site is translated into.
func (l *Localization) Supported() []string { return l.supported }

// Fallback is the language used when nothing else matches.
func (l *Localization) Fallback() string { return l.fallback }

type contextKey int

const localizerKey contextKey = iota

// carrier is what the middleware puts in the context: the localizer and the
// language it was resolved to.
type carrier struct {
	localizer *i18n.Localizer
	lang      string
}

// Middleware resolves the visitor's language and attaches a localizer to the
// request, so no handler has to think about language again.
//
// Resolution order: the choice the visitor made (carried by the cookie), then
// the browser's Accept-Language, then the fallback.
//
// **The address never carries a language.** A link somebody sends is read in
// the receiver's language, not the sender's, and a page's address stays the
// same in every language.
func (l *Localization) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := l.resolve(r)
		ctx := context.WithValue(r.Context(), localizerKey, carrier{
			localizer: i18n.NewLocalizer(l.bundle, lang, l.fallback),
			lang:      lang,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (l *Localization) resolve(r *http.Request) string {
	if cookie, err := r.Cookie(LanguageCookie); err == nil && l.isSupported(cookie.Value) {
		return cookie.Value
	}
	if lang := l.fromAcceptLanguage(r.Header.Get("Accept-Language")); lang != "" {
		return lang
	}
	return l.fallback
}

// languageCookieAge is how long the server-set copy lives. The script renews
// it on every page, so this only matters for a browser running none.
const languageCookieAge = 365 * 24 * 3600

// Switch handles the language switcher's form: `POST /language` with `lang`
// and the page to go back to in `next`.
//
// It is the path for a browser without the script. With the script, the
// submission is intercepted, the choice goes to localStorage, and this is
// never asked. Either way the cookie ends up holding the choice and the
// reader lands back on the page they were reading, at an address with no
// language in it.
//
// An empty or unknown `lang` clears the choice, which hands the decision back
// to the browser's own settings.
func (l *Localization) Switch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}

	cookie := &http.Cookie{
		Name:     LanguageCookie,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	}
	if lang := r.PostFormValue("lang"); l.isSupported(lang) {
		cookie.Value, cookie.MaxAge = lang, languageCookieAge
	} else {
		cookie.MaxAge = -1
	}
	http.SetCookie(w, cookie)

	next := SafeNext(r.PostFormValue("next"))
	if next == "" {
		next = "/"
	}
	// 303, so the page that follows is fetched with GET and a reload does
	// not post the switch again.
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (l *Localization) isSupported(lang string) bool {
	for _, s := range l.supported {
		if s == lang {
			return true
		}
	}
	return false
}

// fromAcceptLanguage picks the best supported match for the browser's header.
func (l *Localization) fromAcceptLanguage(header string) string {
	if header == "" {
		return ""
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil {
		return ""
	}
	for _, tag := range tags {
		base, _ := tag.Base()
		if l.isSupported(base.String()) {
			return base.String()
		}
	}
	return ""
}

// LocalizerFrom returns the request's localizer, or nil outside a localized
// request.
func LocalizerFrom(ctx context.Context) *i18n.Localizer {
	c, _ := ctx.Value(localizerKey).(carrier)
	return c.localizer
}

// LanguageFrom returns the language resolved for the request.
func LanguageFrom(ctx context.Context) string {
	c, _ := ctx.Value(localizerKey).(carrier)
	return c.lang
}
