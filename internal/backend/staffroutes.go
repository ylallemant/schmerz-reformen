package backend

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerStaffRoutes(api huma.API) {
	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-me",
		Method:      http.MethodGet,
		Path:        "/v1/staff/me",
		Summary:     "Say what the signed-in editor may do",
		Description: "The backend's own reading of the identity the console sent: whether it " +
			"is an administrator's, and which collectives it manages. The console draws its " +
			"navigation from this rather than working it out a second time, so the two cannot " +
			"disagree about who may see what.",
		Tags: []string{"Console"},
	}), a.staffMe)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-audit",
		Method:      http.MethodGet,
		Path:        "/v1/staff/audit",
		Summary:     "Read the audit log",
		Description: "Every write made through the console, newest first. An administrator " +
			"reads all of it; an editor reads the history of the collectives they manage.",
		Tags: []string{"Console"},
	}), a.staffAudit)
}

// StaffMeOutput is what an editor may do.
type StaffMeOutput struct {
	Body struct {
		Subject string `json:"subject"`
		Name    string `json:"name,omitempty"`

		// Admin is sent explicitly: a console that read "absent" as "no" and
		// a console that read it as "unchanged" would draw different menus.
		Admin bool `json:"admin"`

		// Groups are the groups the backend decided by: the directory's, once
		// one is provisioned. The console keeps them to draw its menus.
		Groups []string `json:"groups"`

		Collectives []CollectiveItem `json:"collectives"`

		// Waiting is how many changes to organisations this editor may vote
		// on now: proposed by somebody else, and not voted on yet.
		Waiting int `json:"waiting"`
	}
}

func (a *API) staffMe(ctx context.Context, _ *struct{}) (*StaffMeOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}

	listing, err := a.staffListCollectives(ctx, nil)
	if err != nil {
		return nil, err
	}

	pending, _, err := a.store.ListChanges(ctx, store.ChangeQuery{
		Statuses: []models.ChangeStatus{models.ChangePending},
		Page:     store.Page{Limit: 500},
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot count the changes waiting for an editor")
		return nil, huma.Error500InternalServerError("cannot read what is waiting")
	}

	out := &StaffMeOutput{}
	out.Body.Subject = who.Subject
	out.Body.Name = who.Name
	out.Body.Admin = who.Admin
	out.Body.Groups = append([]string{}, who.Groups...)
	out.Body.Collectives = listing.Body.Collectives
	for _, change := range a.changeItems(ctx, who, pending) {
		if change.CanVote {
			out.Body.Waiting++
		}
	}
	return out, nil
}

// AuditInput pages through the log.
type AuditInput struct {
	Limit  int `query:"limit" doc:"how many to return; the default is 100"`
	Offset int `query:"offset"`
}

// AuditItem is one entry of the log.
type AuditItem struct {
	ID           string    `json:"id"`
	At           time.Time `json:"at"`
	Actor        string    `json:"actor"`
	ActorName    string    `json:"actor_name,omitempty"`
	Action       string    `json:"action"`
	SubjectType  string    `json:"subject_type"`
	SubjectID    string    `json:"subject_id"`
	CollectiveID string    `json:"collective_id,omitempty"`
	Collective   string    `json:"collective,omitempty"`
	Summary      string    `json:"summary,omitempty"`
}

// AuditOutput is a page of the log.
type AuditOutput struct {
	Body struct {
		Entries []AuditItem `json:"entries"`
		Total   int64       `json:"total"`
	}
}

func (a *API) staffAudit(ctx context.Context, in *AuditInput) (*AuditOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}

	query := store.AuditQuery{Page: store.Page{Limit: in.Limit, Offset: in.Offset}}
	if !who.Admin {
		managed, _, err := a.store.ListCollectives(ctx, store.CollectiveQuery{
			AuthGroups: append([]string{}, who.Groups...),
			Page:       store.Page{Limit: 500},
		})
		if err != nil {
			log.Error().Err(err).Msg("cannot read the collectives an editor manages")
			return nil, huma.Error500InternalServerError("cannot read the log")
		}
		// Never nil for somebody who is not an administrator: nil is the
		// whole log.
		query.CollectiveIDs = []string{}
		for _, collective := range managed {
			query.CollectiveIDs = append(query.CollectiveIDs, collective.ID)
		}
	}

	entries, total, err := a.store.ListAudit(ctx, query)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the audit log")
		return nil, huma.Error500InternalServerError("cannot read the log")
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.CollectiveID)
	}
	owners := a.ownersOf(ctx, ids)

	out := &AuditOutput{}
	out.Body.Total = total
	out.Body.Entries = make([]AuditItem, 0, len(entries))
	for _, entry := range entries {
		out.Body.Entries = append(out.Body.Entries, AuditItem{
			ID:           entry.ID,
			At:           entry.CreatedAt,
			Actor:        entry.Actor,
			ActorName:    entry.ActorName,
			Action:       string(entry.Action),
			SubjectType:  entry.SubjectType,
			SubjectID:    entry.SubjectID,
			CollectiveID: entry.CollectiveID,
			// Empty for a collective that has since been deleted, which is
			// when the entry's own summary earns its keep.
			Collective: owners[entry.CollectiveID].Name,
			Summary:    entry.Summary,
		})
	}
	return out, nil
}
