package console

import (
	"context"
	"errors"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// The setup wizard: how a console that has no identity provider gets one.
//
// # On the maintenance port, never the application port
//
// The maintenance port is never published, so reaching the page that decides
// who administers this site needs the cluster — a VPN and a port forward. A
// secret can be copied out of a log; a network boundary cannot.
//
// # Absent once provisioned
//
// A provisioned console does not register the route at all, unless it was
// started with --superuser. Absent rather than refusing.
//
// # It needs a human with a token
//
// A first run cannot sign anybody in by OIDC, because the application it would
// sign in against is what the wizard creates. So an operator pastes an
// Authentik API token, which the backend spends once and does not keep.

// SetupPath is where the wizard lives on the maintenance port.
const SetupPath = "/setup"

// setupTemplate is parsed once. Standalone rather than part of the console's
// layout: this page runs before anything is configured, on a port where the
// theme and the console's navigation are not served.
var setupTemplate = template.Must(template.ParseFS(assets, "templates/setup/setup.html"))

// SetupDoor is whether this console offers the wizard, and why.
//
// Two halves that must not be confused: "nothing is configured yet" is the
// ordinary first run and needs no flag; "something is configured and an
// operator deliberately came back to replace it" is the only act that can take
// a working site away from the directory that administers it.
type SetupDoor struct {
	// Open says the wizard is registered at all.
	Open bool

	// Reprovision says an identity provider is configured and this console
	// means to replace it. The backend refuses the write without it.
	Reprovision bool
}

// How long to keep asking the backend at startup, and how often: both
// services come up together on a first run, and whichever wins the race is
// not something either controls.
const (
	setupAskTimeout  = 30 * time.Second
	setupAskInterval = time.Second
)

// askWithRetry waits out a cold start — and only that. A backend that answers
// at all is up: a 500 from it is broken rather than starting, and asking it
// again for thirty seconds changes nothing but how long the console takes to
// fail.
func askWithRetry(ctx context.Context, backend *apiclient.Client) (apiclient.AuthSettings, error) {
	deadline := time.Now().Add(setupAskTimeout)
	for attempt := 1; ; attempt++ {
		settings, err := backend.AuthSettings(ctx)
		if err == nil {
			return settings, nil
		}
		if !starting(err) || time.Now().After(deadline) {
			return settings, err
		}
		if attempt == 1 {
			log.Info().Msg("waiting for the backend before deciding whether to offer setup")
		}
		select {
		case <-ctx.Done():
			return settings, err
		case <-time.After(setupAskInterval):
		}
	}
}

// starting reports whether an error is a backend not listening yet.
func starting(err error) bool {
	var dial *net.OpError
	return errors.As(err, &dial)
}

// decideSetupDoor works out whether the wizard belongs on this console. An
// error means it could not tell, and the door stays shut for now.
//
// # Shut until the backend positively says otherwise
//
// The wizard opens on exactly one answer: the backend saying no identity
// provider is configured. Nothing else can produce that answer — not a
// backend that cannot be reached, not one that refuses, not one that has no
// such route — so a backend having a bad moment can never reopen setup on a
// site that is configured. With --superuser it is opened anyway: the operator
// is at a terminal having asked for exactly that, and the backend still
// refuses an overwrite that does not say so.
func decideSetupDoor(ctx context.Context, backend *apiclient.Client, superuser bool) (SetupDoor, error) {
	settings, err := askWithRetry(ctx, backend)
	if err != nil {
		if superuser {
			log.Warn().Err(err).Msg("cannot ask the backend whether an identity provider is configured; " +
				"--superuser was given, so the setup wizard is open anyway")
			return SetupDoor{Open: true, Reprovision: true}, nil
		}
		return SetupDoor{}, err
	}

	switch {
	case !settings.Provisioned:
		log.Warn().Msg("no identity provider is configured: the setup wizard is open on the " +
			"maintenance port, which is not published — reach it through the cluster")
		return SetupDoor{Open: true}, nil
	case superuser:
		log.Warn().Str("instance", settings.InstanceURL).Msg(
			"SUPERUSER MODE: an identity provider is configured and the setup wizard is open to replace it")
		return SetupDoor{Open: true, Reprovision: true}, nil
	default:
		log.Info().Str("instance", settings.InstanceURL).
			Msg("identity provider configured; the setup wizard is not registered")
		return SetupDoor{}, nil
	}
}

// whyUndecided says, in an operator's words, why the backend could not be
// asked — the cause, where the error alone points at the wrong half.
func whyUndecided(err error) string {
	switch status := apiclient.StatusOf(err); {
	case status == http.StatusNotFound:
		// The backend answered and has no such route. Found on a cluster
		// where the console had been updated and the backend not yet: the
		// error said "404 Not Found" and nothing else.
		return "the backend has no /v1/console/auth: it is older than this console, or --backend-url " +
			"points at another service — deploy both from the same release"
	case status == http.StatusUnauthorized:
		return "the backend refused the console: --staff-token is not the same on both"
	case status == http.StatusServiceUnavailable:
		return "the backend has no --staff-token, so it believes no console — set the same one on both"
	case starting(err):
		return "the backend cannot be reached at --backend-url"
	}
	return "the backend could not answer"
}

// settleRetryInterval is how often an undecided console asks again. A
// variable so a test need not wait for it.
var settleRetryInterval = 15 * time.Second

// settle finishes what startup could not: deciding whether to offer the
// wizard, and connecting the sign-in.
//
// # Why it keeps asking rather than giving up
//
// Startup asks once, and a Kubernetes rollout is exactly when the answer is
// unavailable: the new console starts while an old backend still answers —
// without the route, or with another token — or before the new one is up. A
// console that decided once would keep its wizard shut and its sign-in
// unconnected until somebody thought to restart it, with nothing on the page
// to say so. So it asks again until it has an answer, and acts on it then.
// The rule above still holds: the wizard opens only when the backend says
// nothing is configured.
func (c *console) settle(ctx context.Context, maintenance *http.ServeMux, superuser bool) {
	backend := c.backend.AsConsole(c.staffToken)
	decided := false
	for attempt := 1; ; attempt++ {
		if !decided {
			door, err := decideSetupDoor(ctx, backend, superuser)
			if err == nil {
				decided = true
				c.setupMu.Lock()
				c.setupDoor = door
				c.setupMu.Unlock()
				c.registerSetup(maintenance)
			} else if attempt == 1 {
				log.Error().Err(err).Str("cause", whyUndecided(err)).
					Msg("cannot ask the backend whether an identity provider is configured; " +
						"the setup wizard stays closed and the console keeps asking")
			} else {
				log.Debug().Err(err).Msg("still cannot ask the backend whether an identity provider is configured")
			}
		}

		if c.currentSignIn() == nil {
			if err := c.connectIdentityProvider(ctx); err != nil {
				if attempt == 1 {
					log.Error().Err(err).Str("cause", whyUndecided(err)).
						Msg("cannot connect to the identity provider: nobody can sign in yet, and the console keeps trying")
				}
			}
		}

		// Done once it knows, and either signs people in or offers the wizard
		// that will make it.
		if decided && (c.currentSignIn() != nil || c.door().Open) {
			if attempt > 1 {
				log.Info().Msg("the console has heard from the backend: sign-in and setup are settled")
			}
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(settleRetryInterval):
		}
	}
}
func (c *console) door() SetupDoor {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()
	return c.setupDoor
}

// registerSetup puts the wizard on the maintenance port, if its door is open.
//
// The maintenance port carries no service middleware — no security headers —
// which is right for /metrics and wrong for a page that renders HTML and takes
// a pasted credential. So the wizard wraps itself.
func (c *console) registerSetup(mux *http.ServeMux) {
	if !c.door().Open {
		return
	}
	// Once: the door can open after startup, and a pattern registered
	// twice panics.
	c.setupMu.Lock()
	registered := c.setupRegistered
	c.setupRegistered = true
	c.setupMu.Unlock()
	if registered {
		return
	}
	headers := web.SecurityHeaders()
	mux.Handle("GET "+SetupPath, headers(http.HandlerFunc(c.wizard)))
	mux.Handle("POST "+SetupPath, headers(http.HandlerFunc(c.wizard)))
	mux.Handle("GET "+SetupPath+"/setup.css", headers(http.HandlerFunc(serveSetupStyles)))

	log.Warn().Str("path", SetupPath).Bool("replacing", c.door().Reprovision).
		Msg("setup wizard registered on the maintenance port")
}

func serveSetupStyles(w http.ResponseWriter, _ *http.Request) {
	styles, err := assets.ReadFile("static/setup.css")
	if err != nil {
		http.NotFound(w, &http.Request{})
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(styles) //nolint:errcheck
}

// wizardPage is what the setup page shows.
type wizardPage struct {
	Reprovision bool
	Error       string
	Missing     []string

	// The form, put back after a refusal.
	AppName       string
	InstanceURL   string
	ConsoleURL    string
	AdminUsername string
	AdminName     string

	// What was done.
	Done          bool
	Result        apiclient.Provisioning
	AdminGroup    string
	CallbackPath  string
	ConnectFailed string
}

func (c *console) wizard(w http.ResponseWriter, r *http.Request) {
	// # This request outlives the server's write deadline, on purpose
	//
	// Provisioning is twenty-odd round trips to somebody else's Authentik,
	// forty seconds measured, and the server's WriteTimeout is thirty. The
	// deadline runs from when the request headers were read, so without this
	// the connection is closed before a byte is written — while the backend
	// carries on and finishes. The page that is shown once, carrying the
	// first admin's only way in, would be written to a socket nobody holds.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	door := c.door()
	page := wizardPage{
		Reprovision:  door.Reprovision,
		AppName:      models.DefaultAppName,
		InstanceURL:  c.authentikURL,
		ConsoleURL:   c.consoleURL,
		CallbackPath: CallbackPath,
	}
	if !door.Open {
		// Closed behind a wizard that finished in this process: the route
		// stays registered until a restart, and refuses until then.
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet {
		c.renderWizard(w, http.StatusOK, page)
		return
	}

	if err := r.ParseForm(); err != nil {
		page.Error = "that form could not be read"
		c.renderWizard(w, http.StatusBadRequest, page)
		return
	}
	request := apiclient.ProvisionRequest{
		InstanceURL:   strings.TrimSpace(r.PostFormValue("instance_url")),
		Token:         strings.TrimSpace(r.PostFormValue("token")),
		AppName:       strings.TrimSpace(r.PostFormValue("app_name")),
		ConsoleURL:    strings.TrimSpace(r.PostFormValue("console_url")),
		AdminUsername: strings.TrimSpace(r.PostFormValue("admin_username")),
		AdminName:     strings.TrimSpace(r.PostFormValue("admin_name")),
		Reprovision:   door.Reprovision,
	}
	if request.AppName == "" {
		request.AppName = models.DefaultAppName
	}
	if request.ConsoleURL == "" {
		request.ConsoleURL = c.consoleURL
	}
	page.AppName, page.InstanceURL, page.ConsoleURL = request.AppName, request.InstanceURL, request.ConsoleURL
	page.AdminUsername, page.AdminName = request.AdminUsername, request.AdminName

	result, err := c.backend.AsConsole(c.staffToken).Provision(r.Context(), request)
	if err != nil {
		// The backend's own sentence, which is Authentik's where there is one:
		// "slug: this field must be unique" tells an operator what to do.
		var refused *apiclient.Error
		if errors.As(err, &refused) {
			page.Error = refused.Message
		} else {
			page.Error = err.Error()
		}
		log.Error().Err(err).Msg("provisioning failed")
		c.renderWizard(w, http.StatusBadGateway, page)
		return
	}
	page.Missing = result.Missing
	if !result.Done {
		// The instance lacks something the wizard will not create. Nothing
		// was made, so there is nothing to undo; the page says what to add.
		c.renderWizard(w, http.StatusOK, page)
		return
	}

	// Connected now, not on the next restart: the console found no provider
	// at startup, and an operator who finished the wizard and still could not
	// sign in would have nothing to say a restart is what they are missing.
	if err := c.connectIdentityProvider(r.Context()); err != nil {
		log.Error().Err(err).Msg("provisioned, but the console cannot reach the provider yet")
		page.ConnectFailed = err.Error()
	}

	// The door closes behind it. The route stays registered until this
	// process restarts — net/http cannot remove a route from a live mux — so
	// the handler refuses from here, and the restart makes it absent.
	c.setupMu.Lock()
	c.setupDoor = SetupDoor{}
	c.setupMu.Unlock()

	page.Done = true
	page.Result = result
	page.AdminGroup = models.AdminGroupName(result.AppName)
	advice := "setup complete: restart the console to close the setup door for good"
	if door.Reprovision {
		advice = "setup complete: restart the console without --superuser to close the setup door for good"
	}
	log.Warn().Str("instance", result.InstanceURL).Str("admin", result.AdminUsername).Msg(advice)
	c.renderWizard(w, http.StatusOK, page)
}

func (c *console) renderWizard(w http.ResponseWriter, status int, page wizardPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Shown once, and it may carry the first admin's way in: not for a
	// cache, a proxy's or the browser's.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := setupTemplate.Execute(w, page); err != nil {
		log.Error().Err(err).Msg("cannot render the setup page")
	}
}
