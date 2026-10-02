package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/rs/zerolog/log"
)

// Renderer executes the templates of one service.
//
// Each page gets its own template set, parsed from the shared templates plus
// that one page file. A single shared set would not work: every page defines
// "content", and in one set the last one parsed would silently win.
//
// Parsing happens once at startup, because a template that fails to parse
// should take the service down immediately rather than surprise a visitor.
type Renderer struct {
	pages map[string]*template.Template
}

// NewRenderer parses every page matching pageGlob, each together with every
// shared template matching sharedGlob. A page is addressed by its file name
// without the extension: templates/landing.html is "landing".
func NewRenderer(fsys fs.FS, sharedGlob, pageGlob string) (*Renderer, error) {
	shared, err := fs.Glob(fsys, sharedGlob)
	if err != nil {
		return nil, fmt.Errorf("find shared templates: %w", err)
	}
	pages, err := fs.Glob(fsys, pageGlob)
	if err != nil {
		return nil, fmt.Errorf("find page templates: %w", err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no page templates matched %q", pageGlob)
	}

	r := &Renderer{pages: make(map[string]*template.Template, len(pages))}
	for _, page := range pages {
		name := strings.TrimSuffix(path.Base(page), path.Ext(page))
		files := append(append([]string{}, shared...), page)

		tmpl, err := template.New(name).Funcs(helpers()).ParseFS(fsys, files...)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", name, err)
		}
		if tmpl.Lookup("layout") == nil {
			return nil, fmt.Errorf("page %s has no layout template", name)
		}
		r.pages[name] = tmpl
	}
	return r, nil
}

// Render writes a page as a full HTML response.
//
// The page is rendered into a buffer first: a template that fails halfway
// would otherwise leave a half-written page and a 200 status on the wire, with
// no way left to report the error.
func (r *Renderer) Render(w http.ResponseWriter, status int, name string, data any) {
	tmpl, ok := r.pages[name]
	if !ok {
		log.Error().Str("page", name).Msg("no such page template")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Error().Err(err).Str("page", name).Msg("cannot render page")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.Debug().Err(err).Str("page", name).Msg("visitor disconnected mid-response")
	}
}

// Defined reports whether a page exists, so a service can check its own
// routing table at startup rather than at the first request.
func (r *Renderer) Defined(name string) bool {
	_, ok := r.pages[name]
	return ok
}

// Page is the data every template receives. A service embeds it in its own
// page data.
//
// Translation is a method rather than a template function so that the
// request's localizer travels with the data: binding a function per request
// would mean cloning the whole template set on every page view.
type Page struct {
	// Title is the translation key for the page title.
	Title string

	// Lang is the language this page is rendered in.
	Lang string

	// Languages are the languages the site offers.
	Languages []string

	// Path is the current request path, for marking the active nav item.
	Path string

	// Nonce authorises this page's own inline scripts under the
	// Content-Security-Policy, and nothing else.
	//
	// **Every inline `<script>` a template renders needs it.** One without is
	// refused by the browser, silently — which is the whole point of the
	// policy and also the way a page breaks when somebody forgets. A test
	// asserts that none is forgotten.
	Nonce string

	// Zone is the time zone dates and times are shown in: the installation's,
	// not the reader's. An action happens at a wall-clock time in a place, and
	// "14:00" on the page has to be the 14:00 on the flyer whoever is reading
	// and wherever from. Nil reads as UTC.
	Zone *time.Location

	localizer *i18n.Localizer
}

// NewPage builds the shared page data from a request.
func NewPage(r *http.Request, l *Localization, titleKey string) Page {
	return Page{
		Title:     titleKey,
		Lang:      LanguageFrom(r.Context()),
		Languages: l.Supported(),
		Path:      r.URL.Path,
		Nonce:     NonceFrom(r.Context()),
		localizer: LocalizerFrom(r.Context()),
	}
}

// T translates a message by id.
//
// A missing translation renders as the id itself rather than as an empty
// space: a visible key is a bug report, a blank is a mystery.
func (p Page) T(id string) string {
	if p.localizer == nil {
		return id
	}
	out, err := p.localizer.Localize(&i18n.LocalizeConfig{MessageID: id})
	if err != nil {
		log.Debug().Str("id", id).Str("lang", p.Lang).Msg("missing translation")
		return id
	}
	return out
}

// Tf translates a message and fills in its template data.
func (p Page) Tf(id string, args ...any) string {
	if p.localizer == nil || len(args)%2 != 0 {
		return id
	}

	data := make(map[string]any, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			return id
		}
		data[key] = args[i+1]
	}

	out, err := p.localizer.Localize(&i18n.LocalizeConfig{MessageID: id, TemplateData: data})
	if err != nil {
		log.Debug().Str("id", id).Str("lang", p.Lang).Msg("missing translation")
		return id
	}
	return out
}

// IsCurrent reports whether a nav path is the one being viewed.
func (p Page) IsCurrent(path string) bool {
	return p.Path == path
}

// IsUnder reports whether the page being viewed is a nav path or sits below
// it, so "Topics" stays marked while one topic is open.
func (p Page) IsUnder(path string) bool {
	if path == "/" {
		return p.Path == "/"
	}
	return p.Path == path || strings.HasPrefix(p.Path, strings.TrimSuffix(path, "/")+"/")
}

func helpers() template.FuncMap {
	return template.FuncMap{
		// dict builds a map inline, for passing several values to a partial.
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict needs an even number of arguments, got %d", len(values))
			}
			out := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings, got %T", values[i])
				}
				out[key] = values[i+1]
			}
			return out, nil
		},

		"inc": func(n int) int { return n + 1 },
		"dec": func(n int) int { return n - 1 },

		// initial is the first letter of a name, for the mark drawn where an
		// organisation has uploaded no logo.
		"initial": func(name string) string {
			for _, r := range strings.TrimSpace(name) {
				return string(r)
			}
			return "?"
		},
	}
}
