package console

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// Choices a form offers. Listed here, in the order a person would look for
// them, rather than fetched: they are the model's own enumerations and change
// only when the code does. Each is a key into the catalogue — `kind.cut`,
// `level.federal` — so the word an editor reads is a translator's.
var (
	statuses       = []string{"draft", "published", "archived"}
	updateStatuses = []string{"draft", "published"}
	topicKinds     = []string{"cut", "reform", "closure", "privatisation", "other"}
	levels         = []string{"federal", "state", "municipal"}
	actionKinds    = []string{"demonstration", "rally", "strike", "meeting", "info", "council", "other"}
	memberKinds    = []string{"union", "party", "association", "initiative", "other"}
)

// placeFrom reads where something is out of a posted form.
//
// The coordinates and the zoom come from hidden fields the map picker fills
// in; the name is a field the editor can type in. A form posted with the
// picker untouched sends back the coordinates it was rendered with, which the
// backend recognises as an unmoved pin and does not look up again.
func placeFrom(r *http.Request) apiclient.Place {
	return apiclient.Place{
		Latitude:  floatField(r, "latitude"),
		Longitude: floatField(r, "longitude"),
		Zoom:      intField(r, "zoom"),
		Place:     strings.TrimSpace(r.FormValue("place")),
	}
}

func floatField(r *http.Request, name string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue(name)), 64)
	if err != nil {
		return 0
	}
	return value
}

func intField(r *http.Request, name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if err != nil {
		return 0
	}
	return value
}

// localLayout is what an <input type="datetime-local"> posts: a wall-clock
// time with no zone, because the browser does not know which one is meant.
const localLayout = "2006-01-02T15:04"

// instantFrom reads a wall-clock time an editor typed and says which instant
// it is.
//
// The zone is the installation's, not the browser's. An editor announcing a
// demonstration in Düsseldorf from a laptop on holiday means 14:00 in
// Düsseldorf, and the zone is what turns that into an instant every calendar
// subscribed to the feed will agree on — including across the two nights a
// year the offset changes.
func instantFrom(value string, zone *time.Location) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	// Some browsers add seconds when the field has a step; they are not
	// wanted and not an error.
	if len(value) > len(localLayout) {
		value = value[:len(localLayout)]
	}

	local, err := time.ParseInLocation(localLayout, value, zone)
	if err != nil {
		return "", errors.New("not a date and time")
	}
	return local.Format(time.RFC3339), nil
}

// localValue renders an instant for a datetime-local field, in the
// installation's zone.
func localValue(t time.Time, zone *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(zone).Format(localLayout)
}

// amountFrom reads a sum of whole euros.
//
// Lenient about how people write money — "100.000.000", "100 000 000",
// "100000000 €" all mean the same number — and strict about the rest: a
// figure that cannot be read is an error, never a zero. Zero means "no figure
// was given", and silently turning a typo into that would publish a cut with
// its amount missing.
func amountFrom(value string) (int64, error) {
	cleaned := strings.NewReplacer(
		".", "", ",", "", " ", "", "\u00a0", "", "\u202f", "", "'", "", "€", "", "EUR", "",
	).Replace(strings.TrimSpace(value))
	if cleaned == "" {
		return 0, nil
	}

	amount, err := strconv.ParseInt(cleaned, 10, 64)
	if err != nil || amount < 0 {
		return 0, errors.New("not an amount")
	}
	return amount, nil
}

// maxLogoBytes mirrors the backend's own limit. The backend is what enforces
// it; this stops a ten-megabyte photograph being read into memory first.
const maxLogoBytes = 1 << 20

// uploadFrom reads an image out of a multipart form.
//
// It returns the bytes and the type the browser said they are. Whether that
// type is acceptable is the backend's decision, and it does not take the
// browser's word for the filename at all: nothing here passes one on.
func uploadFrom(w http.ResponseWriter, r *http.Request, field string) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+64<<10)
	if err := r.ParseMultipartForm(maxLogoBytes); err != nil {
		return nil, "", errors.New("the file is too large or could not be read")
	}

	file, header, err := r.FormFile(field)
	if err != nil {
		return nil, "", errors.New("no file was chosen")
	}
	defer file.Close() //nolint:errcheck

	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil {
		return nil, "", errors.New("the file could not be read")
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		// A browser that did not say falls back to the extension — which is
		// only a hint about what to call the bytes, and the backend still
		// refuses anything that is not an image type.
		contentType = mime.TypeByExtension(strings.ToLower(path.Ext(header.Filename)))
	}
	return data, contentType, nil
}
