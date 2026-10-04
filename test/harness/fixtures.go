package harness

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/passkey"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// fixtures is what --seeding puts into a fresh run.
//
// A file rather than Go literals, because it is content: somebody adding a
// topic to it is thinking about what a collective would publish, not about
// structs.
//
// **Every organisation in it is invented and says so in its name.** Fixtures
// that named a real union or a real party would be putting words in its
// mouth, on a site whose whole worth is that what a collective says here is
// what it said.
//
//go:embed fixtures.json
var fixtures []byte

// SeedPlace is where something is.
type SeedPlace struct {
	Place     string  `json:"place"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Zoom      int     `json:"zoom"`
}

// SeedOrganisation is one organisation, created as the site's administrators
// create one.
type SeedOrganisation struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Website string `json:"website"`

	// Parent names an organisation listed earlier in the file.
	Parent string `json:"parent"`

	SeedPlace
}

// SeedUpdate is one piece of news on a topic.
type SeedUpdate struct {
	Title string `json:"title"`
	Body  string `json:"body"`

	// DaysAgo is unused by the API — an update is published when it is
	// published — and is kept in the file as a note of the order they are
	// meant to read in. They are sent oldest first so the feed comes out in
	// that order.
	DaysAgo int `json:"days_ago"`
}

// SeedTopic is one thing a collective campaigns on.
type SeedTopic struct {
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Level     string `json:"level"`
	Summary   string `json:"summary"`
	Body      string `json:"body"`
	Amount    int64  `json:"amount"`
	SourceURL string `json:"source_url"`

	// Status defaults to published; a fixture says "draft" to leave something
	// for the console to show that the public site does not.
	Status string `json:"status"`

	SeedPlace
	Updates []SeedUpdate `json:"updates"`
}

// SeedAction is one thing to come to.
type SeedAction struct {
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Description string `json:"description"`

	// InDays and At date the action from the run rather than from the file,
	// so a seeded demonstration is always still to come however old this file
	// gets. Hours gives it an end; zero leaves it open.
	InDays int    `json:"in_days"`
	At     string `json:"at"`
	Hours  int    `json:"hours"`

	// Topic names the topic it is about, by title.
	Topic string `json:"topic"`

	// Cancelled announces the action and then calls it off, which is the only
	// way an action becomes cancelled: it exercises the same path an editor
	// takes.
	Cancelled bool `json:"cancelled"`

	SeedPlace
}

// SeedCollective is one alliance and everything it published.
type SeedCollective struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Website     string `json:"website"`
	Contact     string `json:"contact"`

	SeedPlace

	// Members name organisations from the file's list, in the order the
	// collective lists them.
	Members []string     `json:"members"`
	Topics  []SeedTopic  `json:"topics"`
	Actions []SeedAction `json:"actions"`
}

// seedFile is the shape of fixtures.json.
type seedFile struct {
	Organisations []SeedOrganisation `json:"organisations"`
	Collectives   []SeedCollective   `json:"collectives"`
}

// SeedSummary is what went in.
type SeedSummary struct {
	Organisations int

	Collectives int
	Topics      int
	Updates     int
	Actions     int

	// Skipped counts what was already there. A run that was seeded before has
	// these collectives at these addresses, and finding them is the seeder
	// working rather than failing.
	Skipped int

	// Reader says whether the passkey round trip worked: an account made with
	// a software authenticator, which then followed something and said it was
	// coming to something.
	Reader bool
}

// Seed fills a run with something to look at.
//
// # Through the API, as the console would
//
// A fixture written straight into a table exercises nothing and would keep
// working long after the path a real editor takes had broken. So everything
// here is sent the way the console sends it: to the content API, with an
// editor's identity. Seeding a run therefore proves that path every time.
//
// # And as a reader would
//
// The last step makes a reader's account by driving the real WebAuthn
// registration with a software authenticator, then follows a collective and
// says it is coming to an action. That is deliberately the long way round:
// registration is the most security-sensitive thing this backend does and the
// least visible from a Go test, because everything interesting normally
// happens in a browser.
//
// # Safe to run twice
//
// A collective whose address is already taken is left alone, with everything
// under it. Resuming a run with --seeding costs a listing and nothing else.
func Seed(ctx context.Context, backendURL, origin, staffToken, zoneName string, out io.Writer) (SeedSummary, error) {
	var file seedFile
	if err := json.Unmarshal(fixtures, &file); err != nil {
		return SeedSummary{}, fmt.Errorf("read the fixtures: %w", err)
	}

	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		return SeedSummary{}, fmt.Errorf("time zone %q: %w", zoneName, err)
	}

	// The development identity, in the administrators' group: the local
	// runner starts the backend with --development and no staff token, which
	// is the one configuration in which an identity is believed on its own.
	identity, err := staffauth.Identity{
		Subject: "seeder", Name: "The seeder", Groups: []string{config.DefaultAdminGroup},
	}.Encode()
	if err != nil {
		return SeedSummary{}, err
	}

	seeder := &seeder{
		base: strings.TrimRight(backendURL, "/"), origin: origin, out: out, zone: zone,
		identity:   identity,
		staffToken: staffToken,
		http:       &http.Client{Timeout: 30 * time.Second},
	}

	existing, err := seeder.existing(ctx)
	if err != nil {
		return SeedSummary{}, fmt.Errorf("read what is already there: %w", err)
	}

	var summary SeedSummary
	organisations, err := seeder.organisations(ctx, file.Organisations, &summary)
	if err != nil {
		return SeedSummary{}, fmt.Errorf("put the organisations in: %w", err)
	}
	seeder.organisationIDs = organisations

	var (

		// What the reader will follow and attend: the first of each that went
		// in, or that was already there.
		followCollective, attendAction string
	)
	for _, collective := range file.Collectives {
		if id, there := existing[collective.Slug]; there {
			summary.Skipped++
			if followCollective == "" {
				followCollective = id
			}
			continue
		}

		id, firstAction, err := seeder.collective(ctx, collective, &summary)
		if err != nil {
			fmt.Fprintf(out, "  collective %q: %v\n", collective.Name, err)
			continue
		}
		summary.Collectives++
		if followCollective == "" {
			followCollective = id
		}
		if attendAction == "" {
			attendAction = firstAction
		}
	}

	if err := seeder.reader(ctx, followCollective, attendAction); err != nil {
		// The content is in, and it is most of the value. Said plainly rather
		// than failing the whole seed — but said, because this is the passkey
		// path and a failure here is worth knowing about.
		fmt.Fprintf(out, "  the reader's passkey round trip failed: %v\n", err)
	} else {
		summary.Reader = true
	}
	return summary, nil
}

type seeder struct {
	base string
	out  io.Writer
	http *http.Client
	zone *time.Location

	// identity is the editor every content call is made as.
	identity string

	// staffToken is the console's secret, when the backend expects one.
	staffToken string

	// origin is what the software authenticator claims the page's origin was.
	// It has to be the frontend's real public address, because the backend
	// checks it against its own configuration — a mismatch here earns exactly
	// the refusal a phishing site would get.
	origin string

	// session is the seeded reader's own token, once it has one.
	session string

	// organisationIDs are the organisations that exist, by name.
	organisationIDs map[string]string
}

// existing reads the collectives already there, by address.
func (s *seeder) existing(ctx context.Context) (map[string]string, error) {
	var answer struct {
		Collectives []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"collectives"`
	}
	if err := s.staff(ctx, http.MethodGet, "/v1/staff/collectives", nil, &answer); err != nil {
		return nil, err
	}

	bySlug := make(map[string]string, len(answer.Collectives))
	for _, collective := range answer.Collectives {
		bySlug[collective.Slug] = collective.ID
	}
	return bySlug, nil
}

// collective puts one alliance in, with its members, topics, updates and
// actions. It returns its identifier and that of its first action.
func (s *seeder) collective(ctx context.Context, collective SeedCollective, summary *SeedSummary) (id, firstAction string, err error) {
	var created struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"name": collective.Name, "slug": collective.Slug,
		"summary": collective.Summary, "description": collective.Description,
		"website": collective.Website, "contact": collective.Contact,
		"status": "published",
	}
	collective.SeedPlace.into(body)

	if err := s.staff(ctx, http.MethodPost, "/v1/staff/collectives", body, &created); err != nil {
		return "", "", err
	}
	if created.ID == "" {
		return "", "", errors.New("no identifier came back")
	}
	base := "/v1/staff/collectives/" + url.PathEscape(created.ID)

	for _, name := range collective.Members {
		organisationID, known := s.organisationIDs[name]
		if !known {
			fmt.Fprintf(s.out, "  member %q: no such organisation in the fixtures\n", name)
			continue
		}
		err := s.staff(ctx, http.MethodPost, base+"/members", map[string]any{
			"organisation_id": organisationID,
		}, nil)
		if err != nil {
			fmt.Fprintf(s.out, "  member %q: %v\n", name, err)
		}
	}

	topics := map[string]string{}
	for _, topic := range collective.Topics {
		topicID, err := s.topic(ctx, base, topic, summary)
		if err != nil {
			fmt.Fprintf(s.out, "  topic %q: %v\n", topic.Title, err)
			continue
		}
		topics[topic.Title] = topicID
		summary.Topics++
	}

	for _, action := range collective.Actions {
		actionID, err := s.action(ctx, base, action, topics[action.Topic])
		if err != nil {
			fmt.Fprintf(s.out, "  action %q: %v\n", action.Title, err)
			continue
		}
		summary.Actions++
		// The first that is actually happening: a reader cannot say they are
		// coming to something that was called off.
		if firstAction == "" && !action.Cancelled {
			firstAction = actionID
		}
	}
	return created.ID, firstAction, nil
}

func (s *seeder) topic(ctx context.Context, base string, topic SeedTopic, summary *SeedSummary) (string, error) {
	status := topic.Status
	if status == "" {
		status = "published"
	}

	body := map[string]any{
		"title": topic.Title, "kind": topic.Kind, "level": topic.Level,
		"summary": topic.Summary, "body": topic.Body, "amount": topic.Amount,
		"source_url": topic.SourceURL, "status": status,
	}
	topic.SeedPlace.into(body)

	var created struct {
		ID string `json:"id"`
	}
	if err := s.staff(ctx, http.MethodPost, base+"/topics", body, &created); err != nil {
		return "", err
	}

	// Oldest first, so the feed reads in the order the file means: the API
	// stamps an update when it is published, and what is sent last is newest.
	for i := len(topic.Updates) - 1; i >= 0; i-- {
		update := topic.Updates[i]
		err := s.staff(ctx, http.MethodPost,
			"/v1/staff/topics/"+url.PathEscape(created.ID)+"/updates",
			map[string]any{"title": update.Title, "body": update.Body, "status": "published"}, nil)
		if err != nil {
			fmt.Fprintf(s.out, "  update %q: %v\n", update.Title, err)
			continue
		}
		summary.Updates++
	}
	return created.ID, nil
}

func (s *seeder) action(ctx context.Context, base string, action SeedAction, topicID string) (string, error) {
	starts, err := s.startOf(action)
	if err != nil {
		return "", err
	}

	body := map[string]any{
		"title": action.Title, "kind": action.Kind, "description": action.Description,
		"topic_id": topicID, "status": "published",
		"starts_at": starts.Format(time.RFC3339),
	}
	if action.Hours > 0 {
		body["ends_at"] = starts.Add(time.Duration(action.Hours) * time.Hour).Format(time.RFC3339)
	}
	action.SeedPlace.into(body)

	var created struct {
		ID string `json:"id"`
	}
	if err := s.staff(ctx, http.MethodPost, base+"/actions", body, &created); err != nil {
		return "", err
	}

	if action.Cancelled {
		// Announced, then called off: the only way an action becomes
		// cancelled, and the path that tells whoever was coming.
		body["cancelled"] = true
		err := s.staff(ctx, http.MethodPut,
			"/v1/staff/actions/"+url.PathEscape(created.ID), body, nil)
		if err != nil {
			return "", fmt.Errorf("cancel: %w", err)
		}
	}
	return created.ID, nil
}

// startOf dates an action from today, at the wall-clock time the fixture
// names, in the installation's zone — the same reading the console makes of
// what an editor types.
func (s *seeder) startOf(action SeedAction) (time.Time, error) {
	days := action.InDays
	if days <= 0 {
		days = 1
	}
	at := action.At
	if at == "" {
		at = "18:00"
	}

	clock, err := time.Parse("15:04", at)
	if err != nil {
		return time.Time{}, fmt.Errorf("the time %q is not HH:MM", at)
	}

	day := time.Now().In(s.zone).AddDate(0, 0, days)
	return time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, s.zone), nil
}

// into writes a place onto a request body. A fixture with no pin sends none,
// and one with a name is not looked up — so seeding makes no request to a
// geocoder.
func (p SeedPlace) into(body map[string]any) {
	body["place"] = p.Place
	if p.Latitude != 0 || p.Longitude != 0 {
		body["latitude"], body["longitude"], body["zoom"] = p.Latitude, p.Longitude, p.Zoom
	}
}

// organisations puts the fixtures' organisations in, as the site's
// administrators create them. It returns every organisation that exists by
// name, including those already there.
//
// An organisation already there by that name is left alone, so a run seeded
// twice does not create them again.
func (s *seeder) organisations(ctx context.Context, seeds []SeedOrganisation, summary *SeedSummary) (map[string]string, error) {
	var answer struct {
		Organisations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organisations"`
	}
	if err := s.staff(ctx, http.MethodGet, "/v1/staff/organisations", nil, &answer); err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(answer.Organisations))
	for _, organisation := range answer.Organisations {
		byName[organisation.Name] = organisation.ID
	}

	for _, seed := range seeds {
		if _, there := byName[seed.Name]; there {
			continue
		}
		body := map[string]any{
			"name": seed.Name, "kind": seed.Kind, "website": seed.Website,
			"parent_id": byName[seed.Parent],
		}
		seed.SeedPlace.into(body)

		var created struct {
			ID string `json:"id"`
		}
		if err := s.staff(ctx, http.MethodPost, "/v1/staff/organisations", body, &created); err != nil {
			fmt.Fprintf(s.out, "  organisation %q: %v\n", seed.Name, err)
			continue
		}
		byName[seed.Name] = created.ID
		summary.Organisations++
	}
	return byName, nil
}

// reader makes an account with a passkey, follows a collective and says it is
// coming to an action.
func (s *seeder) reader(ctx context.Context, collectiveID, actionID string) error {
	device, err := passkey.NewSoftKey(s.origin)
	if err != nil {
		return err
	}

	var begun passkey.Ceremony
	if err := s.public(ctx, http.MethodPost, "/v1/accounts/register/begin",
		map[string]any{"name": "Camille"}, &begun); err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	credential, err := device.Create(begun.Options)
	if err != nil {
		return err
	}

	var signedIn struct {
		Session string `json:"session"`
	}
	finish := map[string]any{
		"ceremony": begun.Ceremony, "credential": credential,
		"device_label": "the seeder",
	}
	if err := s.public(ctx, http.MethodPost, "/v1/accounts/register/finish", finish, &signedIn); err != nil {
		return fmt.Errorf("finish: %w", err)
	}
	if signedIn.Session == "" {
		return errors.New("no session came back")
	}
	s.session = signedIn.Session

	if collectiveID != "" {
		path := "/v1/follows/collective/" + url.PathEscape(collectiveID)
		if err := s.public(ctx, http.MethodPut, path, nil, nil); err != nil {
			return fmt.Errorf("follow: %w", err)
		}
	}
	if actionID != "" {
		path := "/v1/actions/" + url.PathEscape(actionID) + "/participation"
		if err := s.public(ctx, http.MethodPut, path, nil, nil); err != nil {
			return fmt.Errorf("attend: %w", err)
		}
	}
	return nil
}

// staff makes a call the way the console does: as an editor.
func (s *seeder) staff(ctx context.Context, method, path string, body, out any) error {
	return s.call(ctx, method, path, body, out, func(req *http.Request) {
		req.Header.Set(staffauth.IdentityHeader, s.identity)
		if s.staffToken != "" {
			req.Header.Set(staffauth.TokenHeader, s.staffToken)
		}
	})
}

// public makes a call the way the frontend does: as a reader, signed in once
// there is a session.
func (s *seeder) public(ctx context.Context, method, path string, body, out any) error {
	return s.call(ctx, method, path, body, out, func(req *http.Request) {
		if s.session != "" {
			req.Header.Set("Authorization", "Bearer "+s.session)
		}
	})
}

func (s *seeder) call(ctx context.Context, method, path string, body, out any, sign func(*http.Request)) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.base+path, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	sign(req)

	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= http.StatusBadRequest {
		answer, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(answer)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
