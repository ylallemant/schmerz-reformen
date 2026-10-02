package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTheMapLibrariesShipInTheBinary.
//
// They are `//go:embed`-ed like every other asset, which is what lets the
// container run with a read-only root filesystem and nothing mounted. A
// missing file here is a map that silently does not load, and only in the
// built image — the pattern is a directory, so a file in the wrong place is
// simply not included and nothing says so.
func TestTheMapLibrariesShipInTheBinary(t *testing.T) {
	for _, want := range []string{
		"thirdparty/leaflet/leaflet.js",
		"thirdparty/leaflet/leaflet.css",
		"thirdparty/leaflet/leaflet.markercluster.js",
		"thirdparty/leaflet/MarkerCluster.css",
		"thirdparty/leaflet/MarkerCluster.Default.css",
		// Leaflet finds these from its own script URL, so they have to sit in
		// `images/` beside it rather than anywhere else that would work.
		"thirdparty/leaflet/images/marker-icon.png",
		"thirdparty/leaflet/images/marker-icon-2x.png",
		"thirdparty/leaflet/images/marker-shadow.png",
		"thirdparty/leaflet/images/layers.png",
		"thirdparty/leaflet/images/layers-2x.png",
	} {
		if _, err := fs.Stat(ThirdPartyFiles(), want); err != nil {
			t.Errorf("%s is not embedded: %v", want, err)
		}
	}
}

// TestTheMapLibrariesAreServedWhereThePagesAskForThem: the layouts link
// /static/third-party/leaflet/…, and a mismatch between that path and this
// handler is a 404 the Content-Security-Policy would not even report.
func TestTheMapLibrariesAreServedWhereThePagesAskForThem(t *testing.T) {
	mux := http.NewServeMux()
	if err := ThirdParty(mux); err != nil {
		t.Fatalf("ThirdParty: %v", err)
	}

	for path, want := range map[string]int{
		"/static/third-party/leaflet/leaflet.js":             http.StatusOK,
		"/static/third-party/leaflet/images/marker-icon.png": http.StatusOK,
		"/static/third-party/leaflet/nothing.js":             http.StatusNotFound,
		// A directory is not a listing.
		"/static/third-party/leaflet/": http.StatusNotFound,
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Errorf("GET %s = %d, want %d", path, recorder.Code, want)
		}
	}
}
