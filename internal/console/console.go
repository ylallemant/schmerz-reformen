// Package console is where all content is managed.
//
// It is an exposed service, so it holds no database credentials and no
// storage credentials: everything it shows and every change it records goes
// through the backend's API. Editors sign in against the identity provider
// over OpenID Connect, and which collectives each may manage is read from the
// groups it reports — the console keeps no list of its own.
package console

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/cli"
	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/service"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
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
	DefaultAppPort         = 8400
	DefaultMaintenancePort = 9400
)

const (
	defaultBackendURL = "http://127.0.0.1:7500"

	// fallbackLanguage is English, as on the public site: a browser that
	// asks only for languages the console does not offer is more likely to
	// read English than German. One that lists German at all gets German.
	fallbackLanguage = "en"
)

// Definition describes the console binary.
func Definition() cli.Definition {
	return cli.Definition{
		Name:  "console",
		Title: "schMERZ-Reformen Console",
		Short: "Content management for schmerz-reformen",
		Long: "The console is where collectives manage what they publish: their profile,\n" +
			"their member organisations, topics, updates and actions.\n" +
			"Editors sign in through the identity provider, and its groups decide who edits what.",
		DefaultAppPort:         DefaultAppPort,
		DefaultMaintenancePort: DefaultMaintenancePort,
		RegisterFlags: func(cmd *cobra.Command) {
			config.RegisterClientFlags(defaultBackendURL)(cmd)
			// The secret that makes this service the console, and the name of
			// the administrators' group: the same keys the backend reads.
			config.RegisterStaffFlags(cmd)
			config.RegisterOIDCFlags(cmd)
			config.RegisterTimezoneFlags(cmd)
			// Where the public site answers, so a published thing can be
			// linked to from the form that edits it.
			config.RegisterSiteFlags(cmd)
		},
		Setup: Setup,
	}
}

// Command returns the console's root command.
func Command(version, commit string) *cobra.Command {
	return cli.Build(Definition(), version, commit)
}

// console holds the service's dependencies.
type console struct {
	// assetVersion busts the browser cache when the embedded CSS or scripts
	// change. Without it a redeploy leaves visitors on the old stylesheet
	// until their cache expires.
	assetVersion string

	renderer     *web.Renderer
	localization *web.Localization

	// backend is the shared handle, carrying no identity. Every content call
	// goes through staff(), which returns a per-request copy that does.
	backend *apiclient.Client

	// overlay serves the active theme's token overrides, and is invalidated
	// whenever the library changes here.
	overlay *web.ThemeOverlay

	// assets serves the active theme's images, falling back to the built-in
	// ones.
	assets *web.ThemeAssetHandler

	// files serves the package files a theme's own tokens reference.
	files *web.ThemeFileHandler

	// siteURL is where the frontend answers.
	siteURL string

	// zone is the time zone an editor's dates and times are read in.
	zone *time.Location

	// staffToken is the secret presented to the backend.
	staffToken string

	// adminGroup is the administrators' group, the same setting the backend
	// reads. The console uses it for one thing only: deciding which menu
	// entries to draw. What an editor may actually do is the backend's
	// decision on every request, and a console that drew an entry it should
	// not have would produce a refusal, not a breach.
	adminGroup string

	// signIn is the OpenID Connect client. Nil in development.
	signIn *signIn

	// sealer signs the console's cookies.
	sealer *sealer

	// secureCookies is whether cookies carry Secure: derived from the
	// console's own URL, because a deployment told one thing and behaving
	// another is exactly the mistake a second setting invites.
	secureCookies bool

	// development records that authentication is off, so the UI can say so
	// rather than leave an editor assuming they are signed in.
	development bool

	// developmentIdentity is who everybody is when authentication is off.
	developmentIdentity staffauth.Identity
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

	oidcConfig, err := config.LoadOIDC(common.Development)
	if err != nil {
		return fmt.Errorf("sign-in configuration: %w", err)
	}
	staffConfig := config.LoadStaff()

	seal, invented, err := newSealer(oidcConfig.SessionSecret)
	if err != nil {
		return fmt.Errorf("session secret: %w", err)
	}

	c := &console{
		assetVersion:  web.AssetVersion(assets, "static"),
		renderer:      renderer,
		localization:  localization,
		backend:       backend,
		overlay:       web.NewThemeOverlay(backend),
		assets:        web.NewThemeAssetHandler(backend),
		files:         web.NewThemeFileHandler(backend),
		siteURL:       config.LoadSiteURL(),
		zone:          zone,
		staffToken:    staffConfig.Token,
		adminGroup:    staffConfig.AdminGroup,
		sealer:        seal,
		secureCookies: strings.HasPrefix(oidcConfig.ConsoleURL, "https://"),
		development:   common.Development,
	}

	if common.Development {
		groups := oidcConfig.DevelopmentGroups
		if len(groups) == 0 {
			groups = []string{staffConfig.AdminGroup}
		}
		c.developmentIdentity = staffauth.Identity{
			Subject: "development", Name: "Development", Groups: groups,
		}
		log.Warn().Strs("groups", groups).
			Msg("the console signs nobody in: every visitor is the development identity")
	} else {
		if invented {
			// Loud, because the symptom is otherwise mysterious: everybody is
			// signed out by every restart, and behind two replicas a sign-in
			// works on one request and fails on the next.
			log.Warn().Msg("no --session-secret is set: using a random one, which signs everybody out on restart and cannot work behind more than one replica")
		}
		if staffConfig.Token == "" {
			log.Warn().Msg("no --staff-token is set: the backend will refuse every change this console sends")
		}

		c.signIn, err = newSignIn(context.Background(), oidcConfig)
		if err != nil {
			return err
		}
		log.Info().Str("issuer", oidcConfig.Issuer).Str("redirect", oidcConfig.RedirectURL).
			Str("groups_claim", oidcConfig.GroupsClaim).
			Msg("editors sign in through the identity provider")
	}

	// Every page this service serves carries a Content-Security-Policy, and
	// every inline script it renders carries the nonce that policy names.
	svc.Use(web.SecurityHeaders())
	// A write has to come from one of this console's own pages. The session
	// cookie is SameSite=Lax, which already withholds it from a cross-site
	// POST; this refuses the request outright, on the browser's own statement
	// of where it came from, so the defence does not rest on one attribute.
	svc.Use(http.NewCrossOriginProtection().Handler)
	svc.Use(c.requireSignIn)

	if err := c.registerRoutes(svc.Mux()); err != nil {
		return err
	}

	// The console can do nothing without the backend, so it should not be
	// sent traffic while it cannot reach it.
	svc.SetReadinessCheck(backend.Ready)

	log.Info().
		Strs("languages", localization.Supported()).
		Str("backend", clientConfig.BackendURL).
		Str("timezone", zone.String()).
		Msg("console ready to serve")
	return nil
}

// registerRoutes puts every route on a router.
//
// It takes the router rather than the service so that a test can build the
// whole surface on one of its own, with no listeners and no metrics behind it.
func (c *console) registerRoutes(mux *http.ServeMux) error {
	static, err := web.StaticHandler(assets, "static")
	if err != nil {
		return err
	}

	mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	// The map library, for the pin picker.
	if err := web.ThirdParty(mux); err != nil {
		return err
	}

	// The shared palette, component styles and toggle, served from the theme
	// package so neither service keeps a copy.
	web.ThemeAssets(mux, c.assetVersion)
	// Linked after the static defaults, so it overrides only what a theme
	// declares and the built-ins remain the fallback.
	mux.Handle("GET /theme/tokens.css", c.overlay)
	mux.Handle("GET /theme/assets/{slot}", c.assets)
	mux.Handle("GET /theme/files/{name}/{path...}", c.files)

	// Uploaded logos, for the previews beside the upload forms.
	mux.Handle("GET /media/{id}", web.NewMediaHandler(c.backend))

	// The language switcher, for a browser without the script. The address
	// never carries a language; see web.Localization.Middleware.
	mux.HandleFunc("POST /language", c.localization.Switch)

	c.registerAuthRoutes(mux)
	c.registerCollectiveRoutes(mux)
	c.registerOrganisationRoutes(mux)
	c.registerTopicRoutes(mux)
	c.registerActionRoutes(mux)
	c.registerThemeRoutes(mux)

	mux.Handle("GET /audit", c.localized(c.audit))

	// Only on a console with authentication off: see development.go.
	if c.development {
		mux.HandleFunc("POST /development/stand-in", c.switchStandIn)
	}
	return nil
}

// localized wraps a page handler with the language middleware.
func (c *console) localized(h http.HandlerFunc) http.Handler {
	return c.localization.Middleware(h)
}

// staff returns a backend client that speaks for whoever is signed in on this
// request.
//
// A per-request copy, never the shared handle: the identity belongs to one
// request, and a client that carried it between them would publish one
// editor's change under another's name.
func (c *console) staff(r *http.Request) *apiclient.Client {
	client, err := c.backend.AsStaff(c.staffToken, identityFrom(r.Context()))
	if err != nil {
		// Encoding an identity cannot realistically fail. If it does, the
		// right client is the one with no identity, which the backend refuses:
		// a refusal is recoverable and a change under no name is not.
		log.Error().Err(err).Msg("cannot encode the editor's identity")
		return c.backend
	}
	return client
}

// page is what every console template receives.
type page struct {
	web.Page

	// AssetVersion is appended to every /static URL, so a changed stylesheet
	// is fetched rather than served from cache.
	AssetVersion string

	// Development shows the banner saying authentication is off.
	Development bool

	// StandIn is which stand-in editor this browser is, and StandIns the
	// ones it can switch to. Development only.
	StandIn  int
	StandIns []int

	// Editor is who is signed in, for the corner of the navigation.
	Editor string

	// Admin is whether the pages only an administrator can use are offered.
	// It decides what is drawn and nothing else: see console.adminGroup.
	Admin bool

	// SiteURL is where the public site answers.
	SiteURL string

	// UsesMap loads the map library on pages with a place to pin.
	UsesMap bool

	// Notice and Problem are the answer to the form that was just posted:
	// what was saved, or the backend's own sentence about why it was not.
	Notice  string
	Problem string
}

func (c *console) newPage(r *http.Request, titleKey string) page {
	base := web.NewPage(r, c.localization, titleKey)
	base.Zone = c.zone

	identity := identityFrom(r.Context())
	data := page{
		Page:         base,
		AssetVersion: c.assetVersion,
		Development:  c.development,
		SiteURL:      c.siteURL,
		Editor:       identity.Name,
		Admin:        slices.Contains(identity.Groups, c.adminGroup),
	}
	if c.development {
		data.StandIn = standInNumber(r)
		data.StandIns = standInNumbers()
	}
	if notice := r.URL.Query().Get("notice"); notice != "" {
		// A key, never text: the value is in a link anybody can write, and a
		// sentence from a query string rendered as the page's own notice is a
		// way to make this console say something it did not.
		data.Notice = noticeKeys[notice]
	}
	return data
}

// noticeKeys are the confirmations a redirect may ask a page to show.
var noticeKeys = map[string]string{
	"saved":   "notice.saved",
	"created": "notice.created",
	"deleted": "notice.deleted",
	"logo":    "notice.logo",

	// A proposal is not a save: the organisation stays as it was until
	// enough other editors approve, and the notice has to say so.
	"proposed":  "notice.proposed",
	"voted":     "notice.voted",
	"withdrawn": "notice.withdrawn",
}

// profile asks the backend which collectives the signed-in editor manages.
//
// Asked rather than worked out here from the groups in the cookie: which
// group manages which collective is the backend's data, and the list is
// exactly the one it will enforce.
func (c *console) profile(r *http.Request) (apiclient.StaffProfile, error) {
	return c.staff(r).StaffMe(r.Context())
}

// fail answers a request the backend refused or could not be reached for.
//
// Not found is not found, and so is "not yours": the backend answers both the
// same way on purpose. Anything else is the backend's trouble and is reported
// as that, with the reason in the log rather than on the page.
func (c *console) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case apiclient.IsNotFound(err):
		c.renderNotFound(w, r)
	case apiclient.StatusOf(err) == http.StatusForbidden:
		data := c.newPage(r, "error.forbidden_title")
		data.Problem = data.T("error.forbidden")
		c.renderer.Render(w, http.StatusForbidden, "problem", data)
	default:
		log.Error().Err(err).Str("path", r.URL.Path).Msg("the backend could not answer the console")
		data := c.newPage(r, "error.backend_title")
		data.Problem = data.T("error.backend")
		c.renderer.Render(w, http.StatusBadGateway, "problem", data)
	}
}

func (c *console) renderNotFound(w http.ResponseWriter, r *http.Request) {
	data := c.newPage(r, "error.notfound_title")
	data.Problem = data.T("error.notfound")
	c.renderer.Render(w, http.StatusNotFound, "problem", data)
}

// problemOf turns a refused write into the sentence to show above the form.
//
// A refusal is the backend's own words: it knows which field and why. A
// failure that is not a refusal is told to the editor as "it did not work",
// with the detail kept for the log — a stack of driver errors above a form
// helps nobody fill it in.
func (c *console) problemOf(p page, err error) string {
	if apiclient.IsRefusal(err) {
		var refused *apiclient.Error
		if errors.As(err, &refused) {
			return p.Tf("error.refused", "Reason", refused.Message)
		}
	}
	log.Error().Err(err).Msg("the backend could not save what the console sent")
	return p.T("error.backend")
}

// redirect sends the browser on after a write, with a confirmation to show.
//
// 303, so the page that follows is fetched with GET whatever the method that
// led to it: reloading it must not post the form again.
func redirect(w http.ResponseWriter, r *http.Request, target, notice string) {
	if notice != "" {
		// The confirmation goes in the query, which has to come before any
		// fragment: appended after one it would be part of the fragment, and
		// the page would scroll nowhere and confirm nothing.
		target, fragment, hasFragment := strings.Cut(target, "#")
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + "notice=" + url.QueryEscape(notice)
		if hasFragment {
			target += "#" + fragment
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func queryEscape(value string) string { return url.QueryEscape(value) }

// maxFormBytes bounds a posted form. The largest field is a body of twenty
// thousand characters; this is several times that and far below anything that
// would be a problem to hold in memory.
const maxFormBytes = 512 << 10

// parseForm reads a posted form within the bound.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return false
	}
	return true
}
