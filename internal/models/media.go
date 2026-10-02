package models

// Media is one uploaded image: a collective's logo, or a member
// organisation's.
//
// The row is the metadata and the bytes are in storage, under Key. Keeping the
// two apart is what lets the backing service be configuration — a directory in
// development, an object store in production — while the database stays small
// enough to back up without thinking about it.
type Media struct {
	Model

	// CollectiveID is whose it is. It scopes who may replace or delete it, and
	// it is what a collective's deletion sweeps up.
	CollectiveID string `gorm:"index;size:36" json:"collective_id"`

	// Key is where the bytes are in storage. Derived from the identifier, never
	// from an uploaded filename: a name somebody else chose has no business
	// becoming a path.
	Key string `gorm:"size:128" json:"-"`

	ContentType string `gorm:"size:64" json:"content_type"`
	Size        int    `json:"size"`
}

// MaxMediaBytes bounds one upload. A logo is a few kilobytes; anything near
// this limit is a photograph that wants resizing before it is put in front of
// somebody on a phone.
const MaxMediaBytes = 1 << 20 // 1 MiB

// MediaTypes are the image types an upload may have, with the extension its
// storage key carries.
//
// SVG is here because a logo is usually one, and it carries a caveat: an SVG
// can contain script, so it is served under a restrictive
// Content-Security-Policy and only ever through an <img>, never inlined.
var MediaTypes = map[string]string{
	"image/svg+xml": ".svg",
	"image/png":     ".png",
	"image/webp":    ".webp",
	"image/jpeg":    ".jpg",
	"image/gif":     ".gif",
}
