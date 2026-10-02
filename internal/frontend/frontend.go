// Package frontend is the public site.
//
// It serves the map, the news feed and the calendar, in every language the
// site is translated into, and the few pages a signed-in reader has of their
// own. It is an exposed service, so it holds no database credentials and no
// storage credentials: everything it shows comes from the backend's API.
package frontend

import (
	"embed"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/cli"
	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/service"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// Assets are the templates, translations and static files, compiled into the
// binary so the container needs no writable path and nothing to mount.
//
//go:embed templates/shared/*.html templates/pages/*.html static locales/*.toml
var assets embed.FS

// Default ports follow the house convention: 8xxx public, 7xxx internal
// tools, 9xxx maintenance. Every service uses a distinct pair so all three run
// side by side in development.
const (
	DefaultAppPort         = 8401
	DefaultMaintenancePort = 9401
)

const (
	defaultBackendURL = "http://127.0.0.1:7500"

	// fallbackLanguage is English: a reader whose browser asks only for
	// languages the site does not offer is more likely to read English than
	// German. A browser that lists German at all still gets German.
	fallbackLanguage = "en"
)

// How much of each listing a page shows.
const (
	landingUpdates = 6
	landingActions = 6
	feedPerPage    = 20
	topicsPerPage  = 60
)

// Definition describes the frontend binary.
func Definition() cli.Definition {
	return cli.Definition{
		Name:  "frontend",
		Title: "schMERZ-Reformen",
		Short: "The public schmerz-reformen site",
		Long: "The frontend serves the map of cuts and reforms, the news feed and the\n" +
			"calendar of actions, in every language the site is translated into.",
		DefaultAppPort:         DefaultAppPort,
		DefaultMaintenancePort: DefaultMaintenancePort,
		RegisterFlags: func(cmd *cobra.Command) {
			config.RegisterClientFlags(defaultBackendURL)(cmd)
			config.RegisterProjectFlags(cmd)
			// Where this site answers. A feed and a calendar carry absolute
			// links, and a session cookie is Secure exactly when this says
			// https.
			config.RegisterSiteFlags(cmd)
			config.RegisterTimezoneFlags(cmd)
			// How much one address may ask of this site, and which hops are
			// believed about who is asking.
			config.RegisterRateLimitFlags(cmd)
		},
		Setup: Setup,
	}
}

// Command returns the frontend's root command.
func Command(version, commit string) *cobra.Command {
	return cli.Build(Definition(), version, commit)
}

// site holds the service's dependencies.
type site struct {
	// assetVersion busts the browser cache when the embedded CSS or scripts
	// change.
	assetVersion string

	renderer     *web.Renderer
	localization *web.Localization
	backend      *apiclient.Client

	// project is where the source of this software can be found.
	project config.Project

	// proxies are the hops whose X-Forwarded-For is believed.
	proxies web.TrustedProxies

	// overlay, assets and files serve the active theme.
	overlay *web.ThemeOverlay
	assets  *web.ThemeAssetHandler
	files   *web.ThemeFileHandler

	// siteURL is this site's own public address, without a trailing slash,
	// and siteName what it calls itself in a feed reader and a calendar.
	siteURL  string
	siteName string

	// zone is the time zone every date and time is shown in: the
	// installation's, never the reader's. See config.RegisterTimezoneFlags.
	zone *time.Location

	// secureCookies is whether the session cookie carries Secure, derived
	// from siteURL.
	secureCookies bool

	development bool
}

// Setup parses the templates, loads the translations and registers the routes.
func Setup(svc *service.Service, common config.Common) error {
	clientConfig, err := config.LoadClient()
	if err != nil {
		return fmt.Errorf("client configuration: %w", err)
	}
	backend, err := apiclient.New(clientConfig.BackendURL)
	if err != nil {
		return err
	}

	projectConfig, err := config.LoadProject()
	if err != nil {
		return fmt.Errorf("project configuration: %w", err)
	}

	renderer, err := web.NewRenderer(assets, "templates/shared/*.html", "templates/pages/*.html")
	if err != nil {
		return err
	}
	localization, err := web.NewLocalization(assets, "locales", fallbackLanguage)
	if err != nil {
		return err
	}

	zone, err := config.LoadTimezone()
	if err != nil {
		return err
	}

	siteURL := config.LoadSiteURL()

	// Which hops may say who a request is from. Resolved once, at startup, so
	// a typo in a range is a refusal to start rather than a limit that
	// silently bounds nobody.
	proxies, err := config.LoadTrustedProxies()
	if err != nil {
		return fmt.Errorf("--%s: %w", config.KeyTrustedProxy, err)
	}

	s := &site{
		siteURL:       siteURL,
		siteName:      config.LoadSiteName(),
		secureCookies: secureFor(siteURL),
		assetVersion:  web.AssetVersion(assets, "static"),
		renderer:      renderer,
		localization:  localization,
		backend:       backend,
		project:       projectConfig,
		zone:          zone,
		overlay:       web.NewThemeOverlay(backend),
		assets:        web.NewThemeAssetHandler(backend),
		files:         web.NewThemeFileHandler(backend),
		development:   common.Development,
		proxies:       proxies,
	}

	// Every page this service serves carries a Content-Security-Policy, and
	// every inline script it renders carries the nonce that policy names.
	svc.Use(web.SecurityHeaders())

	// A write has to come from one of this site's own pages. The session
	// cookie is SameSite=Lax, which already withholds it from a cross-site
	// POST; this refuses such a request outright, on the browser's own
	// statement of where it came from.
	svc.Use(http.NewCrossOriginProtection().Handler)

	// Bounded per address, on every route. Outside the handlers and inside
	// the policy, so a refused request still carries the headers and never
	// reaches the backend it was going to cost.
	bounds := config.LoadBounds()
	log.Info().
		Int("assets", bounds.Assets).Int("pages", bounds.Pages).
		Int("writes", bounds.Writes).
		Msg("per-address limits, in requests a minute")
	svc.Use(web.RateLimited(bounds, s.proxies))

	if err := s.registerRoutes(svc.Mux()); err != nil {
		return err
	}

	// Nothing here can be shown without the backend, so the site should not
	// be sent traffic while it cannot reach it.
	svc.SetReadinessCheck(backend.Ready)

	log.Info().
		Strs("languages", localization.Supported()).
		Str("backend", clientConfig.BackendURL).
		Str("timezone", zone.String()).
		Msg("frontend ready to serve")
	return nil
}

// registerRoutes puts every route on a router.
//
// It takes the router rather than the service so that a test can build the
// whole surface on one of its own, with no listeners and no metrics behind it.
func (s *site) registerRoutes(mux *http.ServeMux) error {
	static, err := web.StaticHandler(assets, "static")
	if err != nil {
		return err
	}

	mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	if err := web.ThirdParty(mux); err != nil {
		return err
	}

	// A service worker's scope is the path it is served from, so the push
	// receiver has to be at the root to see the whole site.
	mux.HandleFunc("GET /sw.js", s.serviceWorker)

	// The shared palette, component styles and toggle, served from the theme
	// package so neither service keeps a copy.
	web.ThemeAssets(mux, s.assetVersion)
	mux.Handle("GET /theme/tokens.css", s.overlay)
	mux.Handle("GET /theme/assets/{slot}", s.assets)
	mux.Handle("GET /theme/files/{name}/{path...}", s.files)

	// Uploaded logos, served from this origin.
	mux.Handle("GET /media/{id}", web.NewMediaHandler(s.backend))

	// The language switcher, for a browser without the script. The address
	// never carries a language; see web.Localization.Middleware.
	mux.HandleFunc("POST /language", s.localization.Switch)

	localized := func(h http.HandlerFunc) http.Handler {
		return s.localization.Middleware(h)
	}

	mux.Handle("GET /{$}", localized(s.landing))
	mux.Handle("GET /about", localized(s.about))

	mux.Handle("GET /topics", localized(s.topics))
	mux.Handle("GET /topics/{id}", localized(s.topic))
	mux.Handle("GET /feed", localized(s.feed))
	mux.Handle("GET /updates/{id}", localized(s.update))
	mux.Handle("GET /calendar", localized(s.calendar))
	mux.Handle("GET /actions/{id}", localized(s.action))
	mux.Handle("GET /collectives", localized(s.collectives))
	mux.Handle("GET /collectives/{slug}", localized(s.collective))

	// What the map script asks for as the reader moves it.
	mux.Handle("GET /api/map", localized(s.mapJSON))

	// The same content for software rather than people: a feed reader and a
	// calendar application. Not localized — there is no reader to ask.
	mux.HandleFunc("GET /feed.xml", s.atomFeed)
	mux.HandleFunc("GET /calendar.ics", s.calendarFile)
	mux.HandleFunc("GET /collectives/{slug}/calendar.ics", s.collectiveCalendarFile)
	mux.HandleFunc("GET /actions/{id}/calendar.ics", s.actionCalendarFile)

	// What a signed-in reader does with a page. Two paths for each rather
	// than one toggle, so a page left open in another tab cannot silently
	// reverse the press made in this one: a button that said "follow" posts a
	// follow, whatever has happened since.
	mux.Handle("POST /follow/{target}/{id}", localized(s.follow))
	mux.Handle("POST /unfollow/{target}/{id}", localized(s.unfollow))
	mux.Handle("POST /actions/{id}/attend", localized(s.attend))
	mux.Handle("POST /actions/{id}/withdraw", localized(s.withdraw))

	mux.Handle("GET /account/signin", localized(s.signin))
	mux.Handle("GET /account", localized(s.account))
	mux.Handle("POST /account", localized(s.saveAccount))
	mux.Handle("GET /account/link", localized(s.linkDevice))
	mux.Handle("GET /account/following", localized(s.following))
	mux.Handle("GET /notifications", localized(s.notifications))
	mux.Handle("POST /notifications", localized(s.saveNotifications))
	s.registerAccountAPI(mux)
	return nil
}

// page is what every frontend template receives.
type page struct {
	web.Page

	// AssetVersion is appended to every /static URL, so a changed stylesheet
	// is fetched rather than served from cache.
	AssetVersion string

	// ProjectURL is where the source of this software can be found. Empty
	// hides the link.
	ProjectURL string

	// SiteName is what this installation calls itself.
	SiteName string

	// UsesMap loads the map library on pages that draw one.
	UsesMap bool

	// SignedIn is whether this request carries a session at all.
	//
	// It decides what the page offers and nothing about what is permitted.
	// It is read from the cookie's presence — no call to the backend — so it
	// can be on every page without making every page a request.
	SignedIn bool

	// Next is this page's own address, so a control that needs a session can
	// send the reader to sign in and bring them back here.
	Next string

	// Heading and Summary are the page's own title and description, for a
	// page that is about one thing: a topic, an action, a collective.
	//
	// They are what a link looks like when somebody sends it — the tab title,
	// and the card a messenger draws under a pasted address. On a site whose
	// purpose is to be passed around, "Topic — schMERZ-Reformen" under every
	// link would be a waste of the one line people actually read.
	Heading string
	Summary string

	// Canonical is this page's own address in full, for that same card.
	Canonical string
}

func (s *site) newPage(r *http.Request, titleKey string) page {
	base := web.NewPage(r, s.localization, titleKey)
	base.Zone = s.zone

	return page{
		Page:         base,
		AssetVersion: s.assetVersion,
		ProjectURL:   s.project.URL,
		SiteName:     s.siteName,
		SignedIn:     signedIn(r),
		Next:         r.URL.RequestURI(),
		Canonical:    s.absolute(r.URL.Path),
	}
}

// describe gives a page about one thing its own title and description.
func (p *page) describe(heading, summary string) {
	p.Heading = heading
	p.Summary = summary
}

// now is the clock, as a variable so a test can hold it still.
var now = time.Now

// SigninHref is where a control that needs a session sends somebody who has
// none: the sign-in page, with the way back attached.
func (p page) SigninHref() string {
	return "/account/signin?next=" + url.QueryEscape(p.Next)
}

// fail answers a page the backend could not provide.
//
// Not found is the common case and is a page of its own — a topic that was
// taken back to draft answers exactly like one that never existed. Anything
// else is the backend's trouble: said plainly, without detail, and logged.
func (s *site) fail(w http.ResponseWriter, r *http.Request, err error) {
	if apiclient.IsNotFound(err) {
		s.renderNotFound(w, r)
		return
	}
	if apiclient.StatusOf(err) == http.StatusUnauthorized {
		// A session the backend no longer knows. The cookie is cleared so the
		// navigation stops offering "sign out" to somebody who is not signed
		// in, and they are sent to sign in with the way back attached.
		s.clearSession(w)
		s.toSignin(w, r, r.URL.RequestURI())
		return
	}

	log.Error().Err(err).Str("path", r.URL.Path).Msg("the backend could not answer the frontend")
	data := s.newPage(r, "error.title")
	s.renderer.Render(w, http.StatusBadGateway, "error", data)
}

func (s *site) renderNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderer.Render(w, http.StatusNotFound, "notfound", s.newPage(r, "notfound.title"))
}

// back sends the reader to the page they pressed a button on.
//
// The destination is a form field, which is to say it comes from the browser,
// so it is held to this site: without that, a form on somebody else's page
// could use this one as a redirector.
func (s *site) back(w http.ResponseWriter, r *http.Request, fallback string) {
	target := safeNext(r.PostFormValue("next"))
	if target == "" {
		target = fallback
	}
	// 303, so the page that follows is fetched with GET and reloading it does
	// not press the button again.
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// absolute turns a path on this site into a full address, for a feed and a
// calendar file, which are read somewhere a relative link means nothing.
func (s *site) absolute(path string) string {
	return s.siteURL + "/" + strings.TrimPrefix(path, "/")
}
