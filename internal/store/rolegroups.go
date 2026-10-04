package store

import (
	"strconv"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// migrateRoleGroups gives every collective and organisation the groups that
// decide who may do what with it, once.
//
// A collective used to name one group, typed by an administrator, whose
// members managed everything about it. It now has two — its administrators
// and its authors — named from its slug; an organisation gets a slug and two
// groups of its own. Rows that already have their groups are left alone, so
// this is a no-op on every start after the first.
//
// The group a collective named before is kept nowhere: who was in it is the
// directory's to say, and an administrator moves those people with the
// console's people controls.
func migrateRoleGroups(db *gorm.DB) error {
	// What the old model left behind: one free-typed group per collective,
	// and the curation of organisations, which their administrators replace.
	//
	// Cleared before anything here reads a row: PostgreSQL's driver caches
	// each statement's plan per connection, and a `SELECT *` planned while
	// the old column existed fails on that connection once it is gone.
	migrator := db.Migrator()
	if migrator.HasColumn(&models.Collective{}, "auth_group") {
		// SQLite refuses to drop a column an index still covers. Plain SQL,
		// because GORM's PostgreSQL migrator writes DROP INDEX with a schema
		// prefix PostgreSQL does not parse; this statement reads the same in
		// both.
		if err := db.Exec("DROP INDEX IF EXISTS idx_collectives_auth_group").Error; err != nil {
			return err
		}
		if err := migrator.DropColumn(&models.Collective{}, "auth_group"); err != nil {
			return err
		}
	}
	for _, table := range []string{"change_votes", "organisation_changes"} {
		if migrator.HasTable(table) {
			if err := migrator.DropTable(table); err != nil {
				return err
			}
		}
	}

	app := appName(db)
	moved := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		var collectives []models.Collective
		if err := tx.Where("admin_group = '' OR admin_group IS NULL").Find(&collectives).Error; err != nil {
			return err
		}
		for _, collective := range collectives {
			admins, authors := models.CollectiveGroups(app, collective.Slug)
			if err := tx.Model(&models.Collective{}).Where("id = ?", collective.ID).
				Updates(map[string]any{"admin_group": admins, "author_group": authors}).Error; err != nil {
				return err
			}
			moved++
		}

		var organisations []models.Organisation
		if err := tx.Where("slug = '' OR slug IS NULL").Order("created_at asc").Find(&organisations).Error; err != nil {
			return err
		}
		for _, organisation := range organisations {
			slug, err := freeOrganisationSlug(tx, organisation.Name)
			if err != nil {
				return err
			}
			admins, members := models.OrganisationGroups(app, slug)
			if err := tx.Model(&models.Organisation{}).Where("id = ?", organisation.ID).
				Updates(map[string]any{"slug": slug, "admin_group": admins, "member_group": members}).Error; err != nil {
				return err
			}
			moved++
		}
		return nil
	})
	if err != nil {
		return err
	}

	if moved > 0 {
		log.Info().Int("rows", moved).Msg("collectives and organisations given their groups")
	}
	return nil
}

// appName is the provisioned application's name, or the default before the
// console's wizard has run.
func appName(db *gorm.DB) string {
	var settings models.AuthSettings
	if err := db.Select("app_name").First(&settings, "id = ?", models.AuthSettingsID).Error; err == nil &&
		settings.AppName != "" {
		return settings.AppName
	}
	return models.DefaultAppName
}

// freeOrganisationSlug is a slug for an organisation called name that no
// other has: -2, -3… after a name already taken, so two branches called the
// same each get groups of their own.
func freeOrganisationSlug(tx *gorm.DB, name string) (string, error) {
	base := models.Slugify(name)
	if base == "" {
		base = "organisation"
	}
	slug := base
	for n := 2; ; n++ {
		var taken int64
		if err := tx.Model(&models.Organisation{}).Where("slug = ?", slug).Count(&taken).Error; err != nil {
			return "", err
		}
		if taken == 0 {
			return slug, nil
		}
		slug = base + "-" + strconv.Itoa(n)
	}
}
