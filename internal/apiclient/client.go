// Package apiclient talks to the backend's API.
//
// The console and the frontend reach all data through here. They hold no
// database credentials and no storage credentials of their own, which is what
// lets them ship as near-empty container images.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// defaultTimeout bounds a call to the backend. A page that waits forever on a
// dependency is worse than a page that reports it is having trouble.
const defaultTimeout = 10 * time.Second

// Client is a handle on the backend API.
type Client struct {
	baseURL string
	http    *http.Client

	// session is the reader's own session token, forwarded as a bearer header.
	//
	// It is empty on the client a service holds and set on the per-request copy
	// As returns. That split is deliberate: the browser's credential belongs to
	// one request, and a client that carried it on a shared handle would leak
	// whoever spoke last into everybody else's page.
	session string

	// staffToken and staffIdentity are what the console sends: the secret that
	// makes it the console, and who is acting. Set on the per-request copy
	// AsStaff returns, for the same reason.
	staffToken    string
	staffIdentity string
}

// New returns a client for the backend at baseURL.
func New(baseURL string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid backend url %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid backend url %q: want scheme://host", baseURL)
	}

	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: defaultTimeout},
	}, nil
}

// As returns a client that speaks for one signed-in reader.
//
// A copy rather than a mutation, and a shallow one: the HTTP client and its
// connection pool are shared, because the pool is the expensive part and
// nothing in it is per-reader.
//
// An empty session gives back a client that sends no credential, which is the
// right answer for a page a reader has not signed in for.
func (c *Client) As(session string) *Client {
	copied := *c
	copied.session = session
	return &copied
}

// AsStaff returns a client that speaks for one editor signed in to the
// console.
//
// The token is the console's and the identity is the editor's; neither is a
// reader's session, and a client carries one kind of credential or the other.
func (c *Client) AsStaff(token string, identity staffauth.Identity) (*Client, error) {
	encoded, err := identity.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode the staff identity: %w", err)
	}

	copied := *c
	copied.session = ""
	copied.staffToken = token
	copied.staffIdentity = encoded
	return &copied, nil
}

// authorize attaches whichever credential this copy speaks with.
//
// Headers rather than a cookie, because the backend is never reached by a
// browser: the web services hold the cookies, the backend holds the rules, and
// no request to the backend is ambiently authenticated.
func (c *Client) authorize(req *http.Request) {
	if c.session != "" {
		req.Header.Set("Authorization", "Bearer "+c.session)
	}
	if c.staffIdentity != "" {
		req.Header.Set(staffauth.IdentityHeader, c.staffIdentity)
		if c.staffToken != "" {
			req.Header.Set(staffauth.TokenHeader, c.staffToken)
		}
	}
}

// Error is the backend refusing something, with the status it refused it with.
//
// The status survives because the services in front need it: a page for a
// topic that does not exist is a 404 page, an editor's form refused for a bad
// address is shown the sentence the backend wrote, and "the backend is down"
// is neither of those.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// StatusOf returns the HTTP status an error carries, or zero for an error
// that never reached the backend's answer — a timeout, a refused connection.
func StatusOf(err error) int {
	var refused *Error
	if errors.As(err, &refused) {
		return refused.Status
	}
	return 0
}

// IsNotFound reports whether the backend answered that there is no such
// thing. A draft answers this way to a reader, deliberately.
func IsNotFound(err error) bool { return StatusOf(err) == http.StatusNotFound }

// IsRefusal reports whether the backend understood the request and declined
// it for a reason the person who made it can act on: a field that is too
// long, an address already taken, something they may not do.
func IsRefusal(err error) bool {
	status := StatusOf(err)
	return status >= 400 && status < 500
}

// backendError reads the backend's own sentence out of a refusal.
func backendError(resp *http.Response) error {
	var problem struct {
		Detail string `json:"detail"`
		Title  string `json:"title"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	message := resp.Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&problem); err == nil {
		switch {
		case problem.Detail != "":
			message = problem.Detail
		case len(problem.Errors) > 0 && problem.Errors[0].Message != "":
			message = problem.Errors[0].Message
		case problem.Title != "":
			message = problem.Title
		}
	}
	return &Error{Status: resp.StatusCode, Message: message}
}

// do performs one call. A nil out discards the answer; a nil body sends none.
func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call backend %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= http.StatusBadRequest {
		return backendError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, "", nil, out)
}

// send performs a call whose answer is not read.
func (c *Client) send(ctx context.Context, method, path, contentType string, body []byte) error {
	return c.do(ctx, method, path, contentType, body, nil)
}

// sendJSON performs a call with a JSON body and decodes a JSON answer.
func (c *Client) sendJSON(ctx context.Context, method, path string, body []byte, out any) error {
	return c.do(ctx, method, path, "application/json", body, out)
}

func (c *Client) postJSON(ctx context.Context, path string, body []byte, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, body, out)
}

// write encodes a value and sends it, decoding the answer into out.
func (c *Client) write(ctx context.Context, method, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode request for %s: %w", path, err)
	}
	return c.sendJSON(ctx, method, path, body, out)
}

// maxAssetBytes bounds a binary answer: a logo, a theme image, a stylesheet.
// The backend refuses an upload over a megabyte; twice that is a bug.
const maxAssetBytes = 2 << 20

// Asset is a binary answer with its type.
type Asset struct {
	ContentType string
	Data        []byte
}

// getAsset fetches a binary answer.
func (c *Client) getAsset(ctx context.Context, path string) (Asset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return Asset{}, fmt.Errorf("build request for %s: %w", path, err)
	}
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return Asset{}, fmt.Errorf("call backend %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return Asset{}, backendError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
	if err != nil {
		return Asset{}, fmt.Errorf("read %s: %w", path, err)
	}
	return Asset{ContentType: resp.Header.Get("Content-Type"), Data: data}, nil
}

// getRaw fetches a binary answer and discards its type.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, error) {
	asset, err := c.getAsset(ctx, path)
	return asset.Data, err
}

// Ready reports whether the backend is reachable, for the readiness probe of
// a service that depends on it.
func (c *Client) Ready(ctx context.Context) error {
	var ignored json.RawMessage
	return c.get(ctx, "/v1/collectives", &ignored)
}
