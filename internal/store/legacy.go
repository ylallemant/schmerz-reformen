package store

import (
	"strings"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// legacyMemberColumns are what a member row carried while an organisation
// lived inside the collective that listed it.
var legacyMemberColumns = []string{"name", "kind", "website", "logo_id"}

// migrateLegacyMembers moves organisations out of member rows into a table of
// their own, once.
//
// Before organisations were shared, every collective kept its own copy of
// each: a name, a kind, a website and a logo on the member row. Each of those
// becomes an organisation, and the member row a link to it. Copies that agree
// on name and website — the same union entered by two alliances — become one
// organisation, which is the point of the change.
//
// They are created directly, without a vote: they were published already, by
// the collectives that listed them, and asking three editors to approve what
// readers have been seeing for months would only be a queue.
//
// It does nothing on a database that never had the old columns, which is
// every database created after this was written.
func migrateLegacyMembers(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasColumn(&models.CollectiveMember{}, "name") {
		return nil
	}

	type legacyMember struct {
		ID           string
		CollectiveID string
		Name         string
		Kind         string
		Website      string
		LogoID       string
	}

	moved := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		var rows []legacyMember
		// COALESCE because the columns were never NOT NULL, and a NULL
		// cannot be read into a string.
		err := tx.Table("collective_members").
			Select("id, collective_id, COALESCE(name, '') AS name, COALESCE(kind, '') AS kind, " +
				"COALESCE(website, '') AS website, COALESCE(logo_id, '') AS logo_id").
			Where("organisation_id IS NULL OR organisation_id = ''").
			Order("created_at asc").
			Scan(&rows).Error
		if err != nil {
			return err
		}

		organisations := map[string]string{}
		listed := map[string]bool{}
		for _, row := range rows {
			key := strings.ToLower(strings.TrimSpace(row.Name)) + "|" + strings.TrimSpace(row.Website)
			id, known := organisations[key]
			if !known {
				kind := models.MemberKind(row.Kind)
				if !kind.Valid() {
					kind = models.MemberOther
				}
				organisation := models.Organisation{OrganisationValues: models.OrganisationValues{
					Name: row.Name, Kind: kind, Website: row.Website, LogoID: row.LogoID,
				}}
				if err := tx.Create(&organisation).Error; err != nil {
					return err
				}
				id = organisation.ID
				organisations[key] = id

				// The logo was the collective's; now it is the
				// organisation's, so deleting the collective no longer
				// takes it.
				if row.LogoID != "" {
					if err := tx.Model(&models.Media{}).Where("id = ?", row.LogoID).
						Updates(map[string]any{"organisation_id": id, "collective_id": ""}).Error; err != nil {
						return err
					}
				}
			}

			// One collective listing the same organisation twice keeps the
			// first.
			pair := row.CollectiveID + "|" + id
			if listed[pair] {
				if err := tx.Delete(&models.CollectiveMember{}, "id = ?", row.ID).Error; err != nil {
					return err
				}
				continue
			}
			listed[pair] = true

			if err := tx.Table("collective_members").Where("id = ?", row.ID).
				Update("organisation_id", id).Error; err != nil {
				return err
			}
			moved++
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Dropped after the copy is committed, and one at a time: a column left
	// behind by an interrupted run is harmless, and the next start finishes
	// the job because there is nothing left to copy.
	for _, column := range legacyMemberColumns {
		if migrator.HasColumn(&models.CollectiveMember{}, column) {
			if err := migrator.DropColumn(&models.CollectiveMember{}, column); err != nil {
				return err
			}
		}
	}
	log.Info().Int("members", moved).
		Msg("member organisations moved into a table of their own")
	return nil
}
