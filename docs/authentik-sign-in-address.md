# A sign-in address of its own — a handover

> **Superseded on 2026-10-15.** The deployment platform now gives a project its
> own sign-in address and brand (`auth.<zone>`), and schmerz-reformen's setup
> binds its recovery flow to that brand. See
> [platform-identity.md](platform-identity.md). What follows is kept for the
> diagnosis, which still holds.

Written for a session that will automate this. It describes the problem as it
was met in production on 2026-10-15, why it happens, and the solution chosen:
schmerz-reformen gets its own Authentik **brand** on its own domain, created
and reconciled by the setup wizard rather than by hand.

Absolute paths are rooted at
`/Users/yann.lallemant/dev/go/src/github/ylallemant/schmerz-reformen`.

---

## 1. The problem

An administrator added somebody from the console's People page. The way in
shown was:

```
https://auth.codersyndicate.cloud/if/flow/doleances-recovery/?flow_token=…
```

Somebody joining schmerz-reformen is sent through a flow named after
doléances, a different project, on a domain that names neither. It works —
the account is in the right directory and the passkey it sets up signs in to
the console — but:

- **It is the wrong name** in front of exactly the people the site is trying
  to bring in.
- **schmerz-reformen's onboarding depends on another project's flow.** If
  whoever runs doléances changes `doleances-recovery`, or deletes it, every
  way in schmerz-reformen hands out changes or breaks with it, and nothing in
  this repository would notice.
- **Every Authentik page a schmerz-reformen editor sees** — sign-in, passkey
  set-up, recovery — carries the shared instance's branding, not this site's.

## 2. Why it happens

Three facts about Authentik, and one decision in this code.

1. **A recovery link is made with the recovery flow of a brand.**
   `POST /api/v3/core/users/{pk}/recovery/` takes no flow: Authentik uses
   `flow_recovery` of the brand that answered the request
   (`authentik.RecoveryLink`, `internal/authentik/authentik.go:775`).
2. **A brand is chosen by the domain a request arrives on.** Authentik picks
   the brand whose `domain` matches the request's host, and falls back to the
   brand marked `default`. Doléances and schmerz-reformen both talk to
   `auth.codersyndicate.cloud`, so they get the same brand — the default one.
3. **The link is built on the host the API was called on**, so it names
   `auth.codersyndicate.cloud` because that is `AuthSettings.InstanceURL`.
4. **This code binds its own recovery flow to a brand only if the brand has
   none** (`bindRecoveryFlowToBrand`, `internal/authentik/recovery.go:410`,
   always the `default=true` brand). Doléances was set up first, so the
   default brand already named `doleances-recovery`, and schmerz-reformen's
   setup — correctly — refused to overwrite another application's choice.
   Its own `schmerz-recovery` flow exists and is used by nothing.

Facts 2 and 3, and the passkey point in §4, are from Authentik's behaviour as
understood here, not from reading the 2025.10 source. **Verify them against
`goauthentik/authentik` at the tag the cluster runs (2025.10.1) before
building on them** — in particular how a brand's `domain` is matched (exact or
suffix, and which wins when several match) and where the recovery view builds
its absolute URL from.

## 3. The solution

**schmerz-reformen gets its own sign-in address**, `auth.schmerz-reformen.de`,
served by the **same** Authentik, with **its own brand** on that domain:

- `domain`: `auth.schmerz-reformen.de`, `default`: false;
- `flow_recovery`: this site's own `schmerz-recovery` flow;
- `flow_authentication`, `flow_invalidation`, `flow_user_settings`: copied
  from the default brand, so signing in works exactly as it does today;
- `branding_title` "schMERZ-Reformen", and optionally `branding_logo` /
  `branding_favicon` pointing at the public site's own logo.

The backend then talks to Authentik **through that address**: the stored
`InstanceURL` becomes `https://auth.schmerz-reformen.de`. Everything follows
from that one setting — the brand that answers, the recovery flow a link is
made with, the host in the link, the OIDC issuer the console discovers
(`AuthSettings.Issuer()`, `internal/models/auth.go:135`), and the page an
editor signs in on.

Doléances and its default brand are untouched. The users, groups,
application and provider are the same objects as today: one directory, two
front doors.

## 4. What changes for people — read this before migrating production

- **Passkeys are bound to the domain they were made on.** WebAuthn ties a
  credential to its relying party id, which Authentik takes from the host. A
  passkey set up on `auth.codersyndicate.cloud` will not be offered on
  `auth.schmerz-reformen.de`. **Every existing editor needs a new way in**
  (People page → new way in) and sets up a second passkey on the new domain.
  Their old one keeps working for doléances.
- **Everybody signs in again once**: the Authentik session cookie belongs to
  the old host.
- **The OIDC issuer changes** to `https://auth.schmerz-reformen.de/application/o/schmerz/`.
  The console reads it from the backend at startup and on every reconnect, so
  nothing is configured by hand — but a console still holding the old issuer
  must be restarted after the switch.

On today's installation that is a handful of people; it gets more expensive
the longer it waits.

## 5. Infrastructure — outside this repository

The cluster (`kubectl --context paris`) routes with **Gateway API**
(Traefik, `GatewayClass traefik-external`) and issues certificates with
**cert-manager** (`ClusterIssuer letsencrypt`). The objects are labelled
`app.kubernetes.io/managed-by: infra`; find where `auth.codersyndicate.cloud`
is declared in that tool's source and add the new host beside it. Not in
`lafranceinsoumise/infrastructure/kubernetes`.

In namespace `authentik-prod`:

| object | today | add |
|---|---|---|
| `Certificate authentik-external` | `auth.codersyndicate.cloud`, `authentik.prod.authentik.external.codersyndicate.cloud` | `auth.schmerz-reformen.de` |
| `HTTPRoute authentik`, `authentik-https` | the same hosts | `auth.schmerz-reformen.de` |
| DNS | — | `auth.schmerz-reformen.de` → the external gateway (`51.15.252.210`); external-dns is installed and may do this from the route |

Authentik itself needs no configuration for an additional host. If its
`AUTHENTIK_HOST`/allowed-hosts style settings are restricted in this
deployment, check them.

**This is a prerequisite the wizard cannot create** — it can only check it
(§6, step 1).

## 6. The automation — what to build in this repository

### Step 1 — the wizard takes the address and checks it

`internal/console/setup.go`, `templates/setup/setup.html`: the "Authentik
instance" field already exists and is pre-filled from `SCHMERZ_OIDC_ISSUER`.
It is the address this site's people will use — so documenting it as "this
site's sign-in address, e.g. https://auth.schmerz-reformen.de" is most of the
UI change. Pasting the shared address still works and keeps today's
behaviour.

Before writing anything, `authentik.Check` should confirm the address answers
as Authentik (`GET /api/v3/root/config/`) — a missing DNS record or
certificate otherwise surfaces as a confusing TLS or 404 error halfway
through.

### Step 2 — ensure this site's brand

New `EnsureBrand(ctx, spec)` in `internal/authentik` (a new file, e.g.
`brand.go`; `authentik.go` is already past a thousand lines):

- find the brand whose `domain` equals the host of the instance URL
  (`GET /core/brands/?domain=…`, compared exactly);
- create it if absent — `default: false`, the default brand's
  authentication / invalidation / user-settings flows, this site's recovery
  flow, the branding title;
- reconcile it if present, reporting each correction like every other object
  (`Repaired`): the recovery flow and the title are this site's; flows an
  operator set by hand on *this* brand are kept, as elsewhere;
- **if the host is the default brand's domain** (an operator pasted the
  shared address), do not create a brand: fall back to today's behaviour,
  binding to the default brand only when it has no recovery flow, and say so
  on the result page.

### Step 3 — bind to this site's brand, not the default one

`bindRecoveryFlowToBrand` and `BrandHasRecoveryFlow`
(`internal/authentik/recovery.go:410`, `authentik.go:981`) hard-code
`/core/brands/?default=true`. Both should act on the brand from step 2. The
rule "never overwrite another brand's recovery flow" stays — it now simply
never applies to this site's own brand.

`provision.go:150–170` decides `RecoveryReady` from the default brand; it
should decide it from this site's brand.

### Step 4 — permissions

Creating and changing a brand needs `authentik_brands.add_brand` and
`change_brand`. Setup runs with the operator's pasted token, which has them.
The **service account** (`internal/authentik/service.go`) only views brands
and should stay that way: the brand is setup's to reconcile, not the
running backend's.

### Step 5 — tests

The fake directory (`internal/backend/fakedirectory_test.go`) answers
`/core/brands/` with one default brand. It needs brands by domain, create and
patch — **and to behave like Authentik where it differs from intuition**: a
lesson from the same day, when the fake answered "nobody" for a group
Authentik refuses to list, and production broke on what the tests passed.

What to pin:

- setup on a new address creates the brand, with this site's recovery flow
  and the default brand's other flows;
- a second run reconciles rather than duplicates, and reports corrections;
- the default brand is never written when a brand of its own exists, even if
  the default has no recovery flow;
- the shared address keeps today's behaviour exactly;
- a way in minted afterwards names the new host and `schmerz-recovery`
  (assert on the link the fake returns, built from the request host as
  Authentik builds it);
- the wizard refuses before writing anything when the address does not
  answer as Authentik.

Check them by mutation, as the rest of the backend's guards were.

### Step 6 — documentation

`README.md` (setup section), and `CLAUDE.md` under *How it is enforced*,
which today says the recovery flow is bound "to the default brand only if the
brand has none".

## 7. Migrating production, once it is built

1. Infrastructure (§5); confirm `https://auth.schmerz-reformen.de/api/v3/root/config/` answers.
2. Deploy the release.
3. Set `SCHMERZ_OIDC_ISSUER=https://auth.schmerz-reformen.de` on the console
   so the wizard offers it; restart the console with `--superuser`
   (`SCHMERZ_SUPERUSER=true`) and run setup again on the maintenance port,
   with an operator API token. It reconciles everything that exists and
   creates the brand.
4. Restart the console without `--superuser`.
5. Mint a new way in for each existing editor from the People page and pass
   them on (§4).
6. In Authentik, `schmerz-recovery` should now be the new brand's recovery
   flow, and `doleances-recovery` still the default brand's.

## 8. Decisions left to whoever builds it

- **The domain.** `auth.schmerz-reformen.de` is the obvious one; it has to
  exist before the first run.
- **Branding images.** Authentik loads `branding_logo` and `branding_favicon`
  by URL. Pointing them at the public site's own logo keeps one image; the
  public site's CSP and caching then apply to Authentik's pages too.
- **A bulk "new way in for everybody"** on the People page, for step 5 of the
  migration. Not needed for a handful of editors.
