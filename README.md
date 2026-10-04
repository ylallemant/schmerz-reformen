# schMERZ-Reformen

> What is being cut, where — and who is fighting it.

*Schmerz* is German for pain. The capitals are the chancellor's.

## What this is

A reform is passed in Berlin and arrives months later as a line in a city's
budget: a hundred million euros less in Düsseldorf, a swimming pool closed, a
bus that now runs every thirty minutes. The people affected meet it three
levels of government down, one city at a time, and each city takes its own cut
for bad luck.

This site puts them on one map.

- **Collectives** — local alliances of unions, opposition parties,
  associations and initiatives — publish what is planned where they are: the
  **topics** (cuts, reforms, closures), with the amount, the source and a pin.
- Each topic has a **news feed** of updates, and each collective announces
  **actions**: demonstrations, rallies, meetings, the council session where the
  decision is taken.
- The **map** shows all of it at once, and the **calendar** says what is next.
- Readers can **follow** a collective or a topic and say they are **coming** to
  an action — with an account that holds no email address, no phone number and
  no password.

One cut is a misfortune. Four hundred on one map are a policy, and the map is
there to show it.

## Status

**First working version.** All three services run end to end, the test suite
passes, and `go run ./test -seeding` brings up a populated local instance.
Nothing has been deployed, and several things are consciously left for later —
see *Deferred* in [CLAUDE.md](CLAUDE.md).

## How it works

### Who writes

Everything on the site is written by a collective's own editors, in the
**console**. There is no table of editors and no roles of the site's own:
every role is a **group in the identity provider** (Authentik, over OpenID
Connect), and the console creates and fills those groups itself.

| role | group | may |
|---|---|---|
| administrator | `schmerz-admins` | create and delete collectives and organisations, add people, choose each collective's administrators, the theme |
| user | `schmerz-users` | use the console and read it — which is what lets a collective or an organisation add them |
| collective administrator | `schmerz-collective-<slug>-admins` | edit the collective, list organisations in it, choose its authors |
| collective author | `schmerz-collective-<slug>-authors` | write its topics, their news, and its actions |
| organisation administrator | `schmerz-organisation-<slug>-admins` | edit the organisation, choose its administrators and members |
| organisation member | `schmerz-organisation-<slug>-members` | belong to it |

A collective's and an organisation's groups are created with it, named from
its address at that moment, and never renamed. Whoever runs a collective or an
organisation picks its people from a list of the console's users on its own
page; the site's administrators add people to that list from **People**.

The console sets itself up in Authentik. On first start it is **locked** —
nobody can sign in — and a setup wizard waits on its *maintenance* port, which
is never published. Given an Authentik address and a one-off API token, it
creates the OAuth2 provider, the application, the administrators' and users'
groups, a recovery flow if Authentik has none, and a **service account** for
the backend — with a role holding only the permissions it needs and a token of
its own; the pasted token is spent once and may expire. From then on **what an editor may
do is read from Authentik by the backend** on every request, never from the
sign-in token, and people are handed a single-use link to set up a passkey —
no password, no email. The directory token stays in the backend; the console
holds nothing but the OAuth client secret.

Content is published without review. The other half of that bargain is the
**audit log**: every change made through the console is recorded against the
name of the person who made it, append-only.

**Organisations are shared.** A union, a party or an initiative is listed by
every collective it belongs to — one local branch can sit in three alliances —
so it is not any one collective's to change. The site's administrators create
and delete organisations; each one is kept up to date by **its own
administrators**. A collective's own list — which organisations it counts
among its members, in which order — is its administrators' to change.

### Who reads

Reading needs no account. The news is also an **Atom feed** (`/feed.xml`) and
the actions an **iCalendar feed** (`/calendar.ics`, and one per collective), so
nobody has to come back to the site to stay informed.

An account exists for following and for saying "I'm coming". It is a **passkey**
and nothing else: a random handle and a set of public keys. What somebody
follows is attached to that handle, not to a person — a list pairing email
addresses with the government's opponents is one this site declines to hold.
What is public is the *count* of followers, never the list.

## Architecture

Three Go services:

| service | role | exposed |
|---|---|---|
| **backend** | API, persistence, storage, every write | no |
| **console** | content management, behind OIDC sign-in | yes |
| **frontend** | the public site | yes |

The backend owns the database and the storage. The two exposed services talk
to it over its API and hold credentials to nothing else — which is what lets
them ship as static, non-root, read-only, shell-less container images. The
console proves it is the console with a shared secret (`--staff-token`); the
backend, not the console, decides what each editor may do.

Every service is cloud-ready on the same terms: route middleware, a served
OpenAPI spec and docs UI, graceful shutdown, liveness and readiness, and
OpenTelemetry metrics on a **maintenance port** kept separate from the
application port.

| service | application | maintenance |
|---|---|---|
| backend | 7500 | 9500 |
| console | 8400 | 9400 |
| frontend | 8401 | 9401 |

### Stack

Go · [Cobra](https://github.com/spf13/cobra) + [Viper](https://github.com/spf13/viper) ·
[zerolog](https://github.com/rs/zerolog) · [Huma v2](https://github.com/danielgtaylor/huma) ·
[GORM](https://gorm.io) · [gocloud.dev/blob](https://gocloud.dev) · OpenTelemetry ·
server-rendered `html/template` with small vanilla-JS islands ·
[Leaflet](https://leafletjs.com) (vendored) · WebAuthn passkeys ·
PostgreSQL in production, SQLite for development.

Both web UIs are in German and English, with file-based catalogues so a
translation never requires touching code.

## Repository layout

```
cmd/<service>/     binaries — wiring only, no application logic
internal/          everything the services are actually made of
  backend/           the API and its rules
  console/           content management
  frontend/          the public site
  models/ store/     the domain and its persistence
  apiclient/         how the two web services reach the backend
  web/ theme/        rendering, i18n, security headers, the shared stylesheet
  syndication/       the Atom and iCalendar renderers
test/              the local runner and its fixtures
build/docker/      the runtime image
.github/workflows/ CI and release
```

## Development

Requires Go (see [go.mod](go.mod)). SQLite is the development database and
needs no setup — the driver is pure Go, so there is no cgo and no toolchain
beyond Go itself.

```sh
go test ./...                 # run the tests
go build ./...                # build everything
go run ./test -seeding        # start all three services, with example content
```

`go run ./test` brings up the backend, the console and the frontend together
with console authentication switched off, and prints where to reach each one:

```
service    application             maintenance             docs
backend    http://localhost:5400   http://localhost:5410   http://localhost:5400/docs
console    http://localhost:5401   http://localhost:5411   http://localhost:5401/docs
frontend   http://localhost:5402   http://localhost:5412   http://localhost:5402/docs
```

Open `localhost`, not `127.0.0.1`: a passkey is bound to a domain, and a
browser refuses an IP address as one.

Each launch writes to its own directory — `test/run/<timestamp>/`, with
`logs/`, `databases/` and `storage/`, and `test/run/latest` pointing at the
newest. `-latest` carries on with the previous run. Ctrl-C stops all three,
giving each its full graceful shutdown.

`-seeding` puts in example organisations and four example collectives with
topics, updates and actions.
It goes through the API the way the console does, and finishes by registering
a reader's passkey with a software authenticator — so every seeded run
exercises the content path and the account path for real. Every organisation
in the fixtures is invented and says so in its name.

Production runs PostgreSQL, and a schema that works on SQLite does not have to
work there. Two ways to check against the real engine:

```sh
# the store and backend tests, each in a throwaway schema of its own (CI does this)
SCHMERZ_TEST_POSTGRES_DSN='postgres://u:p@localhost:5432/db?sslmode=disable' go test ./...

# the whole stack, with the backend on PostgreSQL — start from an empty database
go run ./test -seeding -postgres 'postgres://u:p@localhost:5432/db?sslmode=disable'
```

Models never pin a column type other than `text`; a test enforces it, because
GORM already picks the right one per engine (`[]byte` is `bytea` on
PostgreSQL, `blob` on SQLite).

`-sso` runs the console as production does, signing editors in through
Authentik instead of `--development` — set `SCHMERZ_OIDC_ISSUER` (in
`test/.env` or the shell) to your Authentik and the setup wizard offers it;
the runner prints where the wizard is. `-superuser` reopens the wizard on a run
that is already set up.

With authentication off everybody is the same stand-in editor, an
administrator by default. To see the console with fewer roles — one
collective's author, say — copy `test/.env.example` to `test/.env` and set
`SCHMERZ_DEVELOPMENT_GROUPS` to the groups above.

## Running it for real

The settings that matter, each available as a flag, an environment variable
(`SCHMERZ_` prefix) or a `config.yaml` field:

| setting | service | what it is |
|---|---|---|
| `site-url` | all | the public address. **Passkeys are bound to its host for ever.** |
| `staff-token` | backend, console | the secret that makes the console the console |
| `settings-key` | backend | seals the stored identity-provider credentials: `openssl rand -base64 32` |
| `admin-group` | backend, console | who administers until the wizard has run; afterwards `<name>-admins` |
| `console-url` | console | its own public address; the wizard registers `{console-url}/auth/callback` |
| `oidc-issuer` | console | the Authentik address the setup wizard offers (an issuer URL is cut back to the instance) |
| `superuser` | console | reopens the setup wizard on a configured console, to point it at another directory |
| `session-secret` | console | signs the console's session cookie |
| `database-driver`, `database-dsn` | backend | `postgres` and a connection URL |
| `storage-url` | backend | where logos are kept: `file://…` or `s3://…` |
| `timezone` | console, frontend | the zone times are entered and shown in (`Europe/Berlin`) |
| `push-public-key`, `push-private-key`, `push-subject` | backend | Web Push; optional |
| `geocode-endpoint`, `geocode-contact` | backend | a Nominatim instance of your own |

In Authentik, nothing by hand: start the console, open
`http://<console>:<maintenance-port>/setup` through the cluster, and paste an
API token (*Directory → Tokens and App passwords*, intent *API Token*). A
collective's group is created in Authentik when an administrator names it.

## Design decisions

[CLAUDE.md](CLAUDE.md) carries the design: the domain model, the permission
model, what an account does and does not hold, and the reasoning behind each.
It is the place to look before proposing a change — most things in it are the
way they are on purpose.

The architecture is taken from
[doléances](https://github.com/ylallemant/doleances), and much of the
infrastructure code with it.

## Licence

[BSD 3-Clause](LICENSE) — © 2026 Yann Lallemant
