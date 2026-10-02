package web

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// MediaSource fetches an uploaded image.
type MediaSource interface {
	Media(ctx context.Context, id string) (apiclient.Asset, error)
}

// MediaHandler serves uploaded logos from this origin.
//
// The backend holds them and is not publicly exposed, so the service a browser
// talks to hands them on — and that is also why a page never has to load an
// image from anywhere but the site itself, which is what the
// Content-Security-Policy insists on.
type MediaHandler struct {
	source MediaSource
}

// NewMediaHandler returns a handler for `GET /media/{id}`.
func NewMediaHandler(source MediaSource) *MediaHandler {
	return &MediaHandler{source: source}
}

func (h *MediaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	asset, err := h.source.Media(r.Context(), id)
	if err != nil {
		if !apiclient.IsNotFound(err) {
			log.Warn().Err(err).Str("media", id).Msg("cannot fetch an uploaded image")
		}
		// A missing logo is a gap in a page, never an error on it.
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", asset.ContentType)
	// For ever: an image is addressed by an identifier that changes whenever
	// the image does, so what a given address answers never changes.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	// An SVG may carry script and this is the site's own origin, so the
	// policy travels with the image wherever it is served from. Set here
	// rather than copied from the backend's answer: this is the response a
	// browser actually sees.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(asset.Data) //nolint:errcheck
}
