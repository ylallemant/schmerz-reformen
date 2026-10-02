package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// thirdParty holds the browser libraries this project did not write: Leaflet
// and its clustering plugin. See thirdparty/leaflet/PROVENANCE.md for where
// they came from and why they are in the repository rather than on a CDN.
//
// They live here, in the package both web services already import, because
// both draw a map — the frontend to show one and the console to aim a pin on
// one — and a copy in each would be two copies to keep at the same version.
//
//go:embed thirdparty
var thirdParty embed.FS

// ThirdPartyPrefix is where the libraries are served. Under /static/, so the
// same caching and the same rate-limit class apply as to every other asset.
const ThirdPartyPrefix = "/static/third-party/"

// ThirdParty serves the vendored libraries on a service's router.
func ThirdParty(mux *http.ServeMux) error {
	handler, err := StaticHandler(thirdParty, "thirdparty")
	if err != nil {
		return err
	}
	mux.Handle("GET "+ThirdPartyPrefix, http.StripPrefix(ThirdPartyPrefix, handler))
	return nil
}

// ThirdPartyFiles exposes the embedded libraries, for the test that checks
// they are all there.
func ThirdPartyFiles() fs.FS { return thirdParty }
