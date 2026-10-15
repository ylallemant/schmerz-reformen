package web

import (
	"embed"
	"net/http"
)

// mapScripts are this project's own additions to Leaflet, shared by both web
// services for the reason the library itself is (see thirdparty.go): both
// draw maps, and a copy in each would be two copies to keep in step. Kept
// apart from thirdparty/ because they are not third-party.
//
//go:embed maps
var mapScripts embed.FS

// MapScriptsPrefix is where they are served, under /static/ like every other
// asset.
const MapScriptsPrefix = "/static/maps/"

// MapScripts serves them on a service's router.
func MapScripts(mux *http.ServeMux) error {
	handler, err := StaticHandler(mapScripts, "maps")
	if err != nil {
		return err
	}
	mux.Handle("GET "+MapScriptsPrefix, http.StripPrefix(MapScriptsPrefix, handler))
	return nil
}
