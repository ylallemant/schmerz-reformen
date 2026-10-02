package backend

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerMediaRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-media",
		Method:      http.MethodGet,
		Path:        "/v1/media/{id}",
		Summary:     "Read an uploaded image",
		Description: "The logo of a collective or of one of its member organisations. Served " +
			"with a policy that neutralises an SVG: an image upload must not be code execution.",
		Tags: []string{"Media"},
	}, a.getMedia)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-put-collective-logo",
		Method:      http.MethodPut,
		Path:        "/v1/staff/collectives/{id}/logo",
		Summary:     "Upload a collective's logo",
		Description: "The image is the request body and its type the Content-Type header. " +
			"Replaces whatever logo was there.",
		Tags: []string{"Console"},
	})), a.staffPutCollectiveLogo)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-delete-collective-logo",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/collectives/{id}/logo",
		Summary:     "Remove a collective's logo",
		Tags:        []string{"Console"},
	})), a.staffDeleteCollectiveLogo)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-put-member-logo",
		Method:      http.MethodPut,
		Path:        "/v1/staff/members/{id}/logo",
		Summary:     "Upload a member organisation's logo",
		Tags:        []string{"Console"},
	})), a.staffPutMemberLogo)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-delete-member-logo",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/members/{id}/logo",
		Summary:     "Remove a member organisation's logo",
		Tags:        []string{"Console"},
	})), a.staffDeleteMemberLogo)
}

// MediaIDInput addresses an uploaded image.
type MediaIDInput struct {
	ID string `path:"id"`
}

// MediaOutput carries an image.
type MediaOutput struct {
	ContentType string `header:"Content-Type"`

	// CacheControl is long. An image is addressed by an identifier that
	// changes whenever the image does — a new upload is a new row — so what a
	// given address answers never changes and may be kept for as long as
	// anybody likes.
	CacheControl string `header:"Cache-Control"`

	// ContentSecurityPolicy neutralises an uploaded SVG. An SVG may contain
	// script and is served from the site's own origin, so this policy is what
	// keeps a logo upload from being code execution in a reader's session.
	ContentSecurityPolicy string `header:"Content-Security-Policy"`
	ContentTypeOptions    string `header:"X-Content-Type-Options"`

	Body []byte
}

func (a *API) getMedia(ctx context.Context, in *MediaIDInput) (*MediaOutput, error) {
	media, err := a.store.Media(ctx, in.ID)
	if errors.Is(err, store.ErrMediaNotFound) {
		return nil, huma.Error404NotFound("no such image")
	}
	if err != nil {
		log.Error().Err(err).Str("media", in.ID).Msg("cannot read an image's record")
		return nil, huma.Error500InternalServerError("cannot read the image")
	}

	data, err := a.storage.Read(ctx, media.Key)
	if err != nil {
		// A row with no bytes behind it. Reported as missing to the reader —
		// which it is — and as an error to the operator, because storage and
		// the database have come apart.
		log.Error().Err(err).Str("media", media.ID).Str("key", media.Key).
			Msg("an image's record exists and its bytes do not")
		return nil, huma.Error404NotFound("no such image")
	}

	return &MediaOutput{
		ContentType:           media.ContentType,
		CacheControl:          "public, max-age=31536000, immutable",
		ContentSecurityPolicy: "default-src 'none'; style-src 'unsafe-inline'; sandbox",
		ContentTypeOptions:    "nosniff",
		Body:                  data,
	}, nil
}

// normaliseMediaType drops any parameters, so "image/svg+xml; charset=utf-8"
// matches.
func normaliseMediaType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// storeImage validates an upload, writes its bytes to storage and records it.
//
// The bytes go first and the row second. The other order would leave, on a
// failed write, a row a page links to with nothing behind it; this order
// leaves at worst a blob nothing refers to, which costs a few kilobytes and
// breaks no page.
func (a *API) storeImage(ctx context.Context, collectiveID, contentType string, data []byte) (models.Media, error) {
	mediaType := normaliseMediaType(contentType)
	extension, ok := models.MediaTypes[mediaType]
	if !ok {
		return models.Media{}, huma.Error422UnprocessableEntity(
			"that is not an image type a logo can have: use SVG, PNG, WebP, JPEG or GIF")
	}
	if len(data) == 0 {
		return models.Media{}, huma.Error422UnprocessableEntity("the file is empty")
	}
	if len(data) > models.MaxMediaBytes {
		return models.Media{}, huma.Error422UnprocessableEntity(
			"the file is too large: a logo may be at most 1 MiB")
	}

	// The key is the identifier and nothing an uploader chose. A filename
	// somebody else supplied has no business becoming a path.
	media := models.Media{
		CollectiveID: collectiveID,
		ContentType:  mediaType,
		Size:         len(data),
	}
	media.ID = models.NewID()
	media.Key = "media/" + media.ID + extension

	if err := a.storage.Write(ctx, media.Key, data); err != nil {
		log.Error().Err(err).Str("key", media.Key).Msg("cannot write an image to storage")
		return models.Media{}, huma.Error500InternalServerError("cannot store the image")
	}
	if err := a.store.CreateMedia(ctx, &media); err != nil {
		log.Error().Err(err).Str("key", media.Key).Msg("cannot record an image")
		a.removeStored(ctx, []string{media.Key})
		return models.Media{}, huma.Error500InternalServerError("cannot store the image")
	}
	return media, nil
}

// dropMedia removes an image: the record, then the bytes. An empty identifier
// is nothing to remove, so callers need not check.
func (a *API) dropMedia(ctx context.Context, id string) {
	if id == "" {
		return
	}
	media, err := a.store.DeleteMedia(ctx, id)
	if errors.Is(err, store.ErrMediaNotFound) {
		return
	}
	if err != nil {
		log.Error().Err(err).Str("media", id).Msg("cannot remove an image's record")
		return
	}
	a.removeStored(ctx, []string{media.Key})
}

// removeStored deletes bytes from storage, logging what it could not remove.
//
// Never an error to the caller: by the time this runs the rows are gone and
// nothing a reader can reach points at these keys. What is left behind on a
// failure is an orphan an operator can sweep, and the log line is how they
// find out.
func (a *API) removeStored(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := a.storage.Delete(ctx, key); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("cannot remove an image from storage; it is now orphaned")
		}
	}
}

// LogoInput is an image upload: the bytes are the body.
type LogoInput struct {
	ID          string `path:"id"`
	ContentType string `header:"Content-Type"`
	RawBody     []byte `contentType:"application/octet-stream"`
}

// LogoOutput says which image is now the logo.
type LogoOutput struct {
	Body struct {
		LogoID string `json:"logo_id"`
	}
}

func (a *API) staffPutCollectiveLogo(ctx context.Context, in *LogoInput) (*LogoOutput, error) {
	who, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	media, err := a.storeImage(ctx, collective.ID, in.ContentType, in.RawBody)
	if err != nil {
		return nil, err
	}

	previous := collective.LogoID
	collective.LogoID = media.ID
	if err := a.store.SaveCollective(ctx, &collective); err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot attach a logo")
		a.dropMedia(ctx, media.ID)
		return nil, huma.Error500InternalServerError("cannot save the logo")
	}
	// Only once the new one is in place: a failed upload must leave the old
	// logo standing rather than no logo at all.
	a.dropMedia(ctx, previous)

	a.audit(ctx, who, models.AuditUpdate, "collective", collective.ID, collective.ID, collective.Name+" (logo)")
	out := &LogoOutput{}
	out.Body.LogoID = media.ID
	return out, nil
}

func (a *API) staffDeleteCollectiveLogo(ctx context.Context, in *CollectiveIDInput) (*DoneOutput, error) {
	who, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if collective.LogoID == "" {
		return done(), nil
	}

	previous := collective.LogoID
	collective.LogoID = ""
	if err := a.store.SaveCollective(ctx, &collective); err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot detach a logo")
		return nil, huma.Error500InternalServerError("cannot remove the logo")
	}
	a.dropMedia(ctx, previous)

	a.audit(ctx, who, models.AuditUpdate, "collective", collective.ID, collective.ID, collective.Name+" (logo removed)")
	return done(), nil
}

func (a *API) staffPutMemberLogo(ctx context.Context, in *LogoInput) (*LogoOutput, error) {
	who, member, err := a.memberFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	media, err := a.storeImage(ctx, member.CollectiveID, in.ContentType, in.RawBody)
	if err != nil {
		return nil, err
	}

	previous := member.LogoID
	member.LogoID = media.ID
	if err := a.store.SaveMember(ctx, &member); err != nil {
		log.Error().Err(err).Str("member", member.ID).Msg("cannot attach a logo")
		a.dropMedia(ctx, media.ID)
		return nil, huma.Error500InternalServerError("cannot save the logo")
	}
	a.dropMedia(ctx, previous)

	a.audit(ctx, who, models.AuditUpdate, "member", member.ID, member.CollectiveID, member.Name+" (logo)")
	out := &LogoOutput{}
	out.Body.LogoID = media.ID
	return out, nil
}

func (a *API) staffDeleteMemberLogo(ctx context.Context, in *MemberIDInput) (*DoneOutput, error) {
	who, member, err := a.memberFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if member.LogoID == "" {
		return done(), nil
	}

	previous := member.LogoID
	member.LogoID = ""
	if err := a.store.SaveMember(ctx, &member); err != nil {
		log.Error().Err(err).Str("member", member.ID).Msg("cannot detach a logo")
		return nil, huma.Error500InternalServerError("cannot remove the logo")
	}
	a.dropMedia(ctx, previous)

	a.audit(ctx, who, models.AuditUpdate, "member", member.ID, member.CollectiveID, member.Name+" (logo removed)")
	return done(), nil
}
