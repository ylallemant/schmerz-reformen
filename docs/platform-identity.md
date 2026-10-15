# Sign-in by the deployment platform — the switch-over

How production moves from the sign-in schMERZ-Reformen's setup wizard made for
itself to the one the deployment platform makes. Decided on 2026-10-15:

- **The platform owns sign-in**: the OIDC client, the static groups
  (`schmerz-admins`, `schmerz-users`, `schmerz-pending`) and this project's own
  sign-in address, `auth.<its zone>`, with a brand of its own.
- **The setup wizard keeps what the platform does not give yet**: the service
  account the backend makes the per-collective and per-organisation groups
  with, manages people with and mints invite links with — and the recovery
  flow, which it binds to this project's own brand. When the platform
  provisions dynamic roles (its plan 15, step 5), the wizard can go.
- **The wizard's old groups are recreated empty**, not adopted: everybody's
  roles are given again afterwards.

## What changed in the code

- **Console** (`config.PlatformClient`, `console.connectIdentityProvider`): with
  `SCHMERZ_OIDC_CLIENT_ID` and `SCHMERZ_OIDC_CLIENT_SECRET` set it signs people in
  with them against `SCHMERZ_OIDC_ISSUER`, asking the backend nothing, and the
  redirect is its own address. One without the other is refused at startup. A
  backend set up for the platform's client refuses a console without one, and
  says which settings to set.
- **Setup** (`backend.provision`, `platformSignIn`): told by the console that
  sign-in is the platform's, it makes no client and no static group. It requires
  the three groups by name — their absence is reported and nothing is created —
  puts the first administrator in `schmerz-admins`, sets up the service account,
  and stores `SignInByPlatform`.
- **The recovery flow** is bound to the brand this site's address is answered by
  (`authentik/brand.go`): the platform's brand on `auth.<zone>`, which carries no
  recovery flow precisely so that the project binds its own. On the shared
  address it is still the default brand, bound only when it has none.

## What changed in the deployment

`transistor-application-schmerz-reformen`:

- `application.yaml` declares the roles — `administrator`, `user`, `pending`
  as static groups; `collective-admin`, `collective-author`,
  `organisation-admin`, `organisation-member` as dynamic patterns — and
  collapses `stage` so there is one identity, named `schmerz`.
- The console reads `issuer`, `client_id`, `client_secret` and the
  administrator group from the identity.

## The switch-over, in order

Nothing here has been run. Each step is an operator's, against shared state.

1. **Release** a schmerz-reformen version with the code above, and put its tag in
   the three `workload.yaml` files of the deployment repository. Push that
   repository: the platform reads what is pushed.
2. **Register the provider** at the platform, as running on it, and its token
   (an Authentik API token of an administrator, *Expiring* unticked):
   ```
   transistor identity provider add codersyndicate --url https://auth.codersyndicate.cloud --served-by authentik/authentik
   printf 'token: %s\n' "$TOKEN" | transistor identity provider token codersyndicate
   ```
3. **Attach the application to it**, named `schmerz` there — the name the
   backend composes every group from:
   ```
   transistor application identity set schmerz-reformen --identity-provider codersyndicate --identity-prefix schmerz
   ```
4. **Remove what the wizard made under those names**, in Authentik, because the
   platform refuses to adopt anything it did not make:
   - the application `schmerz` and its OAuth2 provider `schmerz console`;
   - the groups `schmerz-admins`, `schmerz-users` and, if it exists,
     `schmerz-pending` — **their members lose their roles**; note who they were.

   Leave `schmerz-service` (the backend's service account, its group and role),
   the `schmerz-recovery` flow, and every per-collective and per-organisation
   group: the platform does not touch dynamic groups.
5. **Reconcile the identity, then the cell, then deploy**:
   ```
   transistor identity reconcile --dry-run
   transistor identity reconcile
   transistor reconcile paris
   go -C …/transistor/binary/cmd/transistor run . environment apply paris --application schmerz-reformen
   ```
   The dry run should show the client `schmerz`, the three groups, and the brand
   on `auth.schmerz-reformen.de`. `environment render paris --application
   schmerz-reformen --strict` before applying shows whether every
   `from: { identity: … }` resolved.
6. **Run setup again** on the console's maintenance port, with
   `SCHMERZ_SUPERUSER=true` on the console for that one restart, and an operator
   API token. The Authentik address it offers is the issuer cut back to the
   instance — `https://auth.schmerz-reformen.de` — which is right: the backend
   must call Authentik there for invite links to be made with this project's
   brand. The page says the sign-in is the platform's. Name the first
   administrator.
7. **Restart the console without `SCHMERZ_SUPERUSER`.**
8. **Give everybody their roles and a new way in.** The groups are new and
   empty, and a passkey is bound to the domain it was made on: everybody who
   used `auth.codersyndicate.cloud` sets one up again on the new address, from a
   link minted on the People page.

## Checking it worked

- An invite link from the People page reads
  `https://auth.schmerz-reformen.de/if/flow/schmerz-recovery/…`.
- Signing in to the console happens on a page under `auth.schmerz-reformen.de`.
- In Authentik, the application `schmerz` and the three groups carry the
  platform's stamp; the brand on `auth.schmerz-reformen.de` names
  `schmerz-recovery`; the default brand still names whatever it named before.
