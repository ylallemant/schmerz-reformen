package backend

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// maxCommentRunes bounds what a voter writes. A reason, not an essay.
const maxCommentRunes = 500

func (a *API) registerChangeRoutes(api huma.API) {
	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-changes",
		Method:      http.MethodGet,
		Path:        "/v1/staff/changes",
		Summary:     "List the changes to organisations",
		Description: "Newest first. `status=pending` (the default) is what waits for votes; " +
			"`status=decided` is what was applied, rejected, withdrawn or could not be carried out.",
		Tags: []string{"Organisations"},
	}), a.staffListChanges)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-change",
		Method:      http.MethodGet,
		Path:        "/v1/staff/changes/{id}",
		Summary:     "Read one change, with its votes",
		Tags:        []string{"Organisations"},
	}), a.staffGetChange)

	// The vote that decides a change is what changes an organisation, and an
	// organisation is in the member list of every collective that has it.
	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-vote-on-change",
		Method:      http.MethodPost,
		Path:        "/v1/staff/changes/{id}/votes",
		Summary:     "Approve or reject a change",
		Description: "Any editor but the change's author, once. A rejection needs a reason. " +
			"The change is applied when it has enough approvals, and closed when it has as " +
			"many rejections.",
		Tags: []string{"Organisations"},
	})), a.staffVoteOnChange)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-withdraw-change",
		Method:      http.MethodPost,
		Path:        "/v1/staff/changes/{id}/withdrawal",
		Summary:     "Withdraw a change you proposed",
		Tags:        []string{"Organisations"},
	}), a.staffWithdrawChange)
}

// VoteItem is one vote, without who cast it beyond their name.
type VoteItem struct {
	VoterName string    `json:"voter_name,omitempty"`
	Approve   bool      `json:"approve"`
	Comment   string    `json:"comment,omitempty"`
	At        time.Time `json:"at"`
}

// ChangeItem is a change on the wire, as one editor sees it.
//
// Identities are names only. Whether the asking editor proposed it, and how
// they voted, is worked out here and sent as such: an OIDC subject is an
// identifier for the backend to compare, not something to hand to every
// console page.
type ChangeItem struct {
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`

	// Fields lists what the change sets. Empty for a deletion.
	Fields []string `json:"fields,omitempty"`

	Before OrganisationValuesItem `json:"before"`
	After  OrganisationValuesItem `json:"after"`

	AuthorName string `json:"author_name,omitempty"`

	// Mine is whether the asking editor proposed it: they may withdraw it,
	// and may not vote on it.
	Mine bool `json:"mine"`

	// MyVote is "approve" or "reject" when the asking editor has voted.
	MyVote string `json:"my_vote,omitempty"`

	// CanVote is whether the asking editor may vote now.
	CanVote bool `json:"can_vote"`

	Votes      []VoteItem `json:"votes"`
	Approvals  int        `json:"approvals"`
	Rejections int        `json:"rejections"`

	// Needed is how many approvals apply it, and how many rejections close
	// it.
	Needed int `json:"needed"`

	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// changeItems renders changes for one editor, naming every parent they
// mention in one query.
func (a *API) changeItems(ctx context.Context, who *staff, changes []models.OrganisationChange) []ChangeItem {
	var values []models.OrganisationValues
	for _, change := range changes {
		values = append(values, change.Before, change.After)
	}
	parents := a.parentsOf(ctx, values...)

	items := make([]ChangeItem, 0, len(changes))
	for _, change := range changes {
		approvals, rejections := change.Tally()
		item := ChangeItem{
			ID:             change.ID,
			OrganisationID: change.OrganisationID,
			Kind:           string(change.Kind),
			Status:         string(change.Status),
			Fields:         change.FieldList(),
			Before:         toValuesItem(change.Before, parents),
			After:          toValuesItem(change.After, parents),
			AuthorName:     change.AuthorName,
			Mine:           change.Author == who.Subject,
			Votes:          make([]VoteItem, 0, len(change.Votes)),
			Approvals:      approvals,
			Rejections:     rejections,
			Needed:         a.approvalsNeeded(),
			Reason:         change.Reason,
			CreatedAt:      change.CreatedAt,
			DecidedAt:      change.DecidedAt,
		}
		for _, vote := range change.Votes {
			item.Votes = append(item.Votes, VoteItem{
				VoterName: vote.VoterName, Approve: vote.Approve,
				Comment: vote.Comment, At: vote.CreatedAt,
			})
			if vote.Voter == who.Subject {
				item.MyVote = "reject"
				if vote.Approve {
					item.MyVote = "approve"
				}
			}
		}
		item.CanVote = change.Status.Open() && !item.Mine && item.MyVote == ""
		items = append(items, item)
	}
	return items
}

// approvalsNeeded is the configured threshold. Never below one: a change that
// needed nobody's agreement would be no curation at all.
func (a *API) approvalsNeeded() int {
	return max(a.approvals, 1)
}

// ChangesInput narrows a listing of changes.
type ChangesInput struct {
	Status       string `query:"status" enum:"pending,decided,all" doc:"pending (the default), decided or all"`
	Organisation string `query:"organisation" doc:"only the changes about this organisation"`
	Limit        int    `query:"limit" doc:"how many to return; the default is 100"`
	Offset       int    `query:"offset"`
}

// ChangesOutput is a listing of changes.
type ChangesOutput struct {
	Body struct {
		Changes []ChangeItem `json:"changes"`
		Total   int64        `json:"total"`
	}
}

func (a *API) staffListChanges(ctx context.Context, in *ChangesInput) (*ChangesOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}

	query := store.ChangeQuery{
		OrganisationID: strings.TrimSpace(in.Organisation),
		Page:           store.Page{Limit: in.Limit, Offset: in.Offset},
	}
	switch in.Status {
	case "", "pending":
		query.Statuses = []models.ChangeStatus{models.ChangePending}
	case "decided":
		query.Statuses = []models.ChangeStatus{
			models.ChangeApplied, models.ChangeRejected, models.ChangeWithdrawn, models.ChangeFailed,
		}
	}

	changes, total, err := a.store.ListChanges(ctx, query)
	if err != nil {
		log.Error().Err(err).Msg("cannot list changes")
		return nil, huma.Error500InternalServerError("cannot list the changes")
	}

	out := &ChangesOutput{}
	out.Body.Total = total
	out.Body.Changes = a.changeItems(ctx, who, changes)
	return out, nil
}

// ChangeIDInput addresses a change.
type ChangeIDInput struct {
	ID string `path:"id"`
}

// ChangeOutput is one change.
type ChangeOutput struct {
	Body ChangeItem
}

func (a *API) staffGetChange(ctx context.Context, in *ChangeIDInput) (*ChangeOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}
	change, err := a.store.Change(ctx, in.ID)
	if err != nil {
		return nil, changeRefusal(err, "cannot read the change")
	}
	return &ChangeOutput{Body: a.changeItems(ctx, who, []models.OrganisationChange{change})[0]}, nil
}

// VoteInput is one editor's vote.
type VoteInput struct {
	ID   string `path:"id"`
	Body struct {
		Approve bool   `json:"approve"`
		Comment string `json:"comment,omitempty" doc:"why; required to reject"`
	}
}

func (a *API) staffVoteOnChange(ctx context.Context, in *VoteInput) (*ChangeOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}
	comment, err := cleanLine("the reason", in.Body.Comment, maxCommentRunes)
	if err != nil {
		return nil, err
	}
	if !in.Body.Approve && comment == "" {
		// "No" with no reason gives the author nothing to fix.
		return nil, huma.Error422UnprocessableEntity("say why you reject it")
	}

	outcome, err := a.store.Vote(ctx, in.ID, models.ChangeVote{
		Voter: who.Subject, VoterName: who.Name, Approve: in.Body.Approve, Comment: comment,
	}, a.approvalsNeeded())
	if err != nil {
		return nil, changeRefusal(err, "cannot record the vote")
	}
	change := outcome.Change

	verb := models.AuditReject
	if in.Body.Approve {
		verb = models.AuditApprove
	}
	a.audit(ctx, who, verb, "organisation", change.OrganisationID, "", changeName(change))

	if outcome.Decided {
		a.settle(ctx, outcome)
	}
	return &ChangeOutput{Body: a.changeItems(ctx, who, []models.OrganisationChange{change})[0]}, nil
}

func (a *API) staffWithdrawChange(ctx context.Context, in *ChangeIDInput) (*ChangeOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}
	outcome, err := a.store.WithdrawChange(ctx, in.ID, who.Subject)
	if err != nil {
		return nil, changeRefusal(err, "cannot withdraw the change")
	}

	a.audit(ctx, who, models.AuditWithdraw, "organisation", outcome.Change.OrganisationID, "",
		changeName(outcome.Change))
	a.settle(ctx, outcome)
	return &ChangeOutput{Body: a.changeItems(ctx, who, []models.OrganisationChange{outcome.Change})[0]}, nil
}

// settle does what a decided change leaves to be done outside the database:
// the images nothing refers to any more, and — for one that was applied — the
// line in the log saying what it did to the organisation.
//
// That line carries the author's name, not the name of whoever cast the last
// vote: it records whose change it was, and the votes beside it say who let
// it through.
func (a *API) settle(ctx context.Context, outcome store.Outcome) {
	for _, id := range outcome.Unused {
		a.dropMedia(ctx, id)
	}

	change := outcome.Change
	switch change.Status {
	case models.ChangeApplied:
		verb := map[models.ChangeKind]models.AuditAction{
			models.ChangeCreate: models.AuditCreate,
			models.ChangeUpdate: models.AuditUpdate,
			models.ChangeDelete: models.AuditDelete,
		}[change.Kind]
		var approvers []string
		for _, vote := range change.Votes {
			if vote.Approve {
				approvers = append(approvers, vote.VoterName)
			}
		}
		author := &staff{}
		author.Subject, author.Name = change.Author, change.AuthorName
		a.audit(ctx, author, verb, "organisation", change.OrganisationID, "",
			changeName(change)+" (approved by "+strings.Join(approvers, ", ")+")")
		log.Info().Str("change", change.ID).Str("kind", string(change.Kind)).
			Str("organisation", change.OrganisationID).Msg("an approved change to an organisation was applied")
	case models.ChangeFailed:
		log.Info().Str("change", change.ID).Str("reason", change.Reason).
			Msg("an approved change to an organisation could not be applied")
	default:
		log.Info().Str("change", change.ID).Str("status", string(change.Status)).
			Msg("a change to an organisation was closed")
	}
}
