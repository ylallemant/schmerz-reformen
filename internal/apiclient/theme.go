package apiclient

import (
	"context"
	"net/http"
	"net/url"
)

// ActiveThemeCSS returns the active theme rendered as CSS override blocks.
//
// The caller serves this after its static defaults. An error is worth
// reporting but never worth failing a page over: the defaults are already
// loaded and remain correct.
func (c *Client) ActiveThemeCSS(ctx context.Context) ([]byte, error) {
	return c.getRaw(ctx, "/v1/theme.css")
}

// ThemeSummary describes one theme in the library.
type ThemeSummary struct {
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	BuiltIn bool   `json:"built_in"`

	// Colors are the theme's colourways and ActiveColor the chosen one.
	Colors      []string `json:"colors"`
	ActiveColor string   `json:"active_color"`

	HasStructure bool `json:"has_structure"`
	Files        int  `json:"files"`

	// Swatches previews a palette; Sample previews a structure.
	Swatches map[string]string `json:"swatches,omitempty"`
	Sample   map[string]string `json:"sample,omitempty"`

	Warnings []ContrastWarning `json:"warnings,omitempty"`
}

// ContrastWarning is one pairing that fails WCAG AA.
type ContrastWarning struct {
	Mode  string  `json:"mode"`
	Pair  string  `json:"pair"`
	Ratio float64 `json:"ratio"`
	Min   float64 `json:"min"`
}

// ListThemes returns the theme library, each theme with its colourways.
func (c *Client) ListThemes(ctx context.Context) ([]ThemeSummary, error) {
	var payload struct {
		Themes []ThemeSummary `json:"themes"`
	}
	if err := c.get(ctx, "/v1/themes", &payload); err != nil {
		return nil, err
	}
	return payload.Themes, nil
}

// DownloadTheme returns a theme as a package.
func (c *Client) DownloadTheme(ctx context.Context, name string) ([]byte, error) {
	return c.getRaw(ctx, "/v1/themes/"+url.PathEscape(name))
}

// UploadTheme adds or replaces a theme, optionally selecting it.
func (c *Client) UploadTheme(ctx context.Context, name string, document []byte, activate bool) error {
	path := "/v1/themes/" + url.PathEscape(name)
	if activate {
		path += "?activate=true"
	}
	return c.send(ctx, http.MethodPut, path, "application/json", document)
}

// SetActiveTheme selects a theme and one of its colourways.
func (c *Client) SetActiveTheme(ctx context.Context, name, colour string) error {
	return c.write(ctx, http.MethodPut, "/v1/themes/active",
		map[string]string{"name": name, "color": colour}, nil)
}

// DeleteTheme removes a theme from the library.
func (c *Client) DeleteTheme(ctx context.Context, name string) error {
	return c.send(ctx, http.MethodDelete, "/v1/themes/"+url.PathEscape(name), "", nil)
}

// ThemeAssetSummary describes one image of a theme without its contents.
type ThemeAssetSummary struct {
	Slot        string `json:"slot"`
	Path        string `json:"path"`
	Description string `json:"description"`
	ContentType string `json:"content_type,omitempty"`
	Size        int    `json:"size,omitempty"`
	Custom      bool   `json:"custom"`
	HasDefault  bool   `json:"has_default"`
}

// ListThemeAssets returns a theme's images.
func (c *Client) ListThemeAssets(ctx context.Context, themeName string) ([]ThemeAssetSummary, error) {
	var payload struct {
		Assets []ThemeAssetSummary `json:"assets"`
	}
	if err := c.get(ctx, "/v1/theme-assets/"+url.PathEscape(themeName), &payload); err != nil {
		return nil, err
	}
	return payload.Assets, nil
}

// UploadThemeAsset replaces one of a theme's images.
func (c *Client) UploadThemeAsset(ctx context.Context, themeName, slot, contentType string, data []byte) error {
	path := "/v1/theme-assets/" + url.PathEscape(themeName) + "/" + url.PathEscape(slot)
	return c.send(ctx, http.MethodPut, path, contentType, data)
}

// DeleteThemeAsset removes an image, falling back to the built-in default.
func (c *Client) DeleteThemeAsset(ctx context.Context, themeName, slot string) error {
	path := "/v1/theme-assets/" + url.PathEscape(themeName) + "/" + url.PathEscape(slot)
	return c.send(ctx, http.MethodDelete, path, "", nil)
}

// ActiveThemeAsset returns an image of whichever theme is active.
func (c *Client) ActiveThemeAsset(ctx context.Context, slot string) (Asset, error) {
	return c.getAsset(ctx, "/v1/theme-asset/"+url.PathEscape(slot))
}

// ThemeFile returns one file from a theme's package.
func (c *Client) ThemeFile(ctx context.Context, name, filePath string) (Asset, error) {
	return c.getAsset(ctx, "/v1/theme-files/"+url.PathEscape(name)+"/"+filePath)
}
