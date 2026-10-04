package backend

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// requiresStaff is the operation metadata key that marks a route as belonging
// to the console.
//
// Declared on the operation and enforced in middleware, for the reason the
// session requirement is: a write route somebody copies must not be able to
// arrive without its check.
const requiresStaff = "staff"

// staffOnly marks an operation as callable only by the console, on behalf of
// somebody signed in to it.
func staffOnly(op huma.Operation) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[requiresStaff] = true
	op.Security = append(op.Security, map[string][]string{"staff": {}})
	return op
}

// requiresConsole is the operation metadata key that marks a route as the
// console's own, made before anybody is signed in: reading how it signs
// people in, and recording what its setup wizard provisioned. The console's
// secret is required and no editor is — there is nobody to name yet.
const requiresConsole = "console"

// consoleOnly marks an operation as callable only by the console itself.
func consoleOnly(op huma.Operation) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[requiresConsole] = true
	op.Security = append(op.Security, map[string][]string{"staff": {}})
	return op
}

// requiresAdmin is the operation metadata key that marks a route as an
// administrator's: something about the installation rather than about one
// collective's content.
const requiresAdmin = "admin"

// adminOnly marks an operation as callable only by an administrator.
//
// Enforced in middleware, and **audited there too**: a successful write on
// such a route is recorded without the handler doing anything. These are the
// changes every reader sees at once — the active theme, the logo in the
// masthead — and "who changed the colours on Tuesday" should not depend on
// whether somebody remembered a line in a handler.
func adminOnly(op huma.Operation) huma.Operation {
	op = staffOnly(op)
	op.Metadata[requiresAdmin] = true
	return op
}

// staffKey is where the resolved editor is kept for the request.
type staffKeyType struct{}

var staffKey staffKeyType

// staff is the editor making a request, with what they may do worked out.
//
// The identity is what the identity provider said about them at sign-in and
// nothing else. The backend keeps no table of editors: whether somebody may
// touch a collective is recomputed on every request from the groups they
// arrived with.
type staff struct {
	staffauth.Identity

	// Admin is membership of the administrators' group: every collective, the
	// theme, and the whole audit log.
	Admin bool
}

// manages reports whether this editor may change a collective's content.
//
// A collective with no group is managed by administrators alone. That is the
// state it is created in, and it must not mean "by anybody": an empty string
// matching an empty list is exactly the kind of accident this line is here to
// rule out.
func (s *staff) manages(collective models.Collective) bool {
	if s.Admin {
		return true
	}
	return collective.AuthGroup != "" && slices.Contains(s.Groups, collective.AuthGroup)
}

// authenticateStaff decides whether a request really comes from the console,
// and who it says is acting.
//
// # Why a shared secret, when the backend is not exposed anyway
//
// "Not exposed" is a property of a deployment; this is a property of the
// request. The frontend reaches this service too, on the same network, and it
// is the service the whole internet talks to. Without the token, a bug there
// that let a reader choose a header would be a reader publishing as an editor.
// With it, the frontend cannot speak as the console because it was never told
// the secret.
//
// # Development
//
// With --development and no token configured, the identity is believed without
// one. That is the local runner. A token that *is* configured is enforced even
// then, so a development flag left on by mistake does not also open the
// content API.
func (a *API) authenticateStaff(ctx huma.Context) (*staff, int, string) {
	if status, reason := a.authenticateConsole(ctx); status != 0 {
		return nil, status, reason
	}

	identity, err := staffauth.Decode(ctx.Header(staffauth.IdentityHeader))
	if err != nil {
		log.Debug().Err(err).Msg("a staff request carried no usable identity")
		return nil, http.StatusUnauthorized, "sign in to the console first"
	}

	// Once the console has provisioned a directory, what somebody may do is
	// the directory's answer, not the console's: see directory.go. A
	// development run's stand-ins carry no username and are believed as they
	// are, which is what lets one person be several editors locally.
	if a.provisioned.Load() && !(a.development && identity.Username == "") {
		status, reason := a.groupsFromDirectory(ctx, &identity)
		if status != 0 {
			return nil, status, reason
		}
	}

	return &staff{
		Identity: identity,
		Admin:    slices.Contains(identity.Groups, a.currentAdminGroup()),
	}, 0, ""
}

// groupsFromDirectory replaces the groups an identity arrived with by the ones
// the directory says it has. A write always asks; a read may use an answer up
// to a minute old.
func (a *API) groupsFromDirectory(ctx huma.Context, identity *staffauth.Identity) (int, string) {
	if identity.Username == "" {
		return http.StatusUnauthorized, "sign in to the console again"
	}

	fresh := ctx.Method() != http.MethodGet
	groups, err := a.directory.rolesOf(ctx.Context(), identity.Username, fresh)
	switch {
	case errors.Is(err, authentik.ErrTokenRefused):
		// The backend's own credential, not anything the editor did: said as
		// the operator's problem. Nobody can act until it is replaced.
		log.Error().Err(err).Msg("the directory refused the backend's token: no editor can act until it is replaced")
		return http.StatusServiceUnavailable,
			"the console's access to the identity provider has been revoked — an operator has to run its setup again"
	case err != nil:
		// A directory that cannot be reached refuses, deliberately: carrying
		// on with what the editor's cookie says is exactly what reading roles
		// from the directory exists to prevent.
		log.Error().Err(err).Str("username", identity.Username).Msg("cannot read an editor's groups from the directory")
		return http.StatusServiceUnavailable, "cannot reach the identity provider to confirm what you may do"
	}
	identity.Groups = groups
	return 0, ""
}

// authenticateConsole decides whether a request really comes from the
// console: its shared secret, or development.
func (a *API) authenticateConsole(ctx huma.Context) (int, string) {
	switch {
	case a.staffToken != "":
		presented := ctx.Header(staffauth.TokenHeader)
		if subtle.ConstantTimeCompare([]byte(presented), []byte(a.staffToken)) != 1 {
			return http.StatusUnauthorized, "this route belongs to the console"
		}
	case a.development:
		// Believed without a secret, deliberately. See authenticateStaff.
	default:
		// No token and not development: there is nothing that could make a
		// request believable, so nothing is believed.
		return http.StatusServiceUnavailable,
			"the content API is switched off: no staff token is configured"
	}
	return 0, ""
}

// staffOf returns the editor making the request, or nil.
func staffOf(ctx context.Context) *staff {
	who, _ := ctx.Value(staffKey).(*staff)
	return who
}

// mustStaff returns the editor on a route that requires one.
//
// The middleware has already refused a request without one, so a nil here is a
// route that forgot to declare itself — answered as a 401 rather than a panic.
func mustStaff(ctx context.Context) (*staff, error) {
	who := staffOf(ctx)
	if who == nil {
		return nil, huma.Error401Unauthorized("sign in to the console first")
	}
	return who, nil
}

// mustAdmin returns the editor on a route only administrators may use.
func mustAdmin(ctx context.Context) (*staff, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}
	if !who.Admin {
		return nil, huma.Error403Forbidden("only an administrator may do that")
	}
	return who, nil
}

// audit records what an editor did.
//
// It never fails the request. The change has already been made by the time
// this runs, and answering "that did not work" for a write that did would send
// an editor to do it twice. A log that could not be written is an operator's
// problem and is reported as one.
func (a *API) audit(ctx context.Context, who *staff, action models.AuditAction, subjectType, subjectID, collectiveID, summary string) {
	err := a.store.Audit(ctx, models.AuditEntry{
		Actor:        who.Subject,
		ActorName:    who.Name,
		Action:       action,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
		CollectiveID: collectiveID,
		Summary:      trimTo(summary, 250),
	})
	if err != nil {
		log.Error().Err(err).
			Str("actor", who.Subject).Str("action", string(action)).
			Str("subject_type", subjectType).Str("subject", subjectID).
			Msg("cannot write the audit log")
	}
}
