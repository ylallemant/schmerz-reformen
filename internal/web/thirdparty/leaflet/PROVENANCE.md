# Vendored map libraries

- **Leaflet 1.9.4** — `leaflet.js`, `leaflet.css`, `images/*`
  from `https://unpkg.com/leaflet@1.9.4/dist/`
- **Leaflet.markercluster 1.5.3** — `leaflet.markercluster.js`,
  `MarkerCluster.css`, `MarkerCluster.Default.css`
  from `https://unpkg.com/leaflet.markercluster@1.5.3/dist/`

Both are BSD-2-Clause; Leaflet's licence is beside this file and
markercluster carries the same terms.

## Why these are in the repository rather than on a CDN

**A CDN sees the address of every reader.** Whoever serves the script learns
the IP of everybody who loads a page — on a site about organising against a
government's reforms, at the moment somebody is looking up where the next
demonstration is. That is the same trade this project refuses everywhere
else: reverse geocoding runs on the backend rather than in the browser for
exactly this reason.

**It would be script on the origin where a session cookie lives.** A
compromise of that CDN or of either package would run with the authority of a
signed-in reader — or, in the console, of a signed-in editor. The session
cookie being `HttpOnly` would not help, because a script does not need to read
it: it can simply ask the browser to register another passkey, or post a form,
and the browser attaches the cookie. That is account takeover from a supply
chain nobody here controls.

A Content-Security-Policy is what refuses foreign script, and a policy that
allowed `unpkg` would be no policy at all. So vendoring is the prerequisite
rather than a tidy-up alongside it.

## Why they are in `internal/web`

Both web services draw a map: the frontend shows one and the console aims a
pin on one. One embedded copy in the package both already import, rather than
a copy in each to keep at the same version.

## Updating

Replace the files from the same paths at a new version, update the versions
above, and check the map pages: the marker images are found by Leaflet from
its own script URL, so they must stay in `images/` beside `leaflet.js`.
