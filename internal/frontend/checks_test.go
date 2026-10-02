package frontend

import (
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/webcheck"
)

const (
	localesDir   = "locales"
	templateGlob = "templates/*/*.html"
)

func TestEveryCatalogueHasEveryKey(t *testing.T) {
	webcheck.EveryCatalogueHasEveryKey(t, assets, localesDir)
}

func TestTranslationsRespectTheLengthBudget(t *testing.T) {
	webcheck.TranslationsRespectTheLengthBudget(t, assets, localesDir)
}

// TestEveryKeyTheSiteAsksForIsDefined, including the ones a template builds
// from a value a reader never chose: the kind of a topic, the kind of an
// action, the kind of a notification.
//
// The values are the model's own enumerations. They are written out here
// rather than imported from the backend, because this service must not link
// the backend — and because a kind the backend gained that this list lacks is
// exactly what should be noticed: it would be shown to a reader as
// `kind.merger`.
func TestEveryKeyTheSiteAsksForIsDefined(t *testing.T) {
	var enumerated []string
	for prefix, values := range map[string][]string{
		"kind":        topicKinds,
		"level":       levels,
		"action_kind": {"demonstration", "rally", "strike", "meeting", "info", "council", "other"},
		"member_kind": {"union", "party", "association", "initiative", "other"},
		"status":      {"archived", "cancelled"},
		"notify": {
			"topic_update", "topic_published", "action_published",
			"action_changed", "action_cancelled",
		},
		"account": {"error_last_passkey", "error_name", "error_general"},
	} {
		for _, value := range values {
			enumerated = append(enumerated, prefix+"."+value)
		}
	}
	for month := 1; month <= 12; month++ {
		enumerated = append(enumerated, "month."+itoa(month), "month_short."+itoa(month))
	}
	for weekday := 0; weekday <= 6; weekday++ {
		enumerated = append(enumerated, "weekday."+itoa(weekday), "weekday_short."+itoa(weekday))
	}
	for _, key := range []string{
		"format.date", "format.date_short", "format.time", "format.datetime",
		"format.money", "format.money_millions", "format.money_billions",
		"format.decimal_mark", "format.thousands_mark",
	} {
		enumerated = append(enumerated, key)
	}

	webcheck.EveryKeyAskedForIsDefined(t, assets, localesDir, templateGlob, enumerated)
}

func TestEveryInlineScriptCarriesTheNonce(t *testing.T) {
	webcheck.EveryInlineScriptCarriesTheNonce(t, assets, templateGlob)
}

// TestNoPageLoadsForeignResources: a resource from elsewhere hands that
// elsewhere the address of everybody reading — on a site where what somebody
// is reading is where the next demonstration against the government is.
func TestNoPageLoadsForeignResources(t *testing.T) {
	webcheck.NoPageLoadsForeignResources(t, assets, templateGlob)
}

func TestNoInlineHandlersOrStyles(t *testing.T) {
	webcheck.NoInlineHandlersOrStyles(t, assets, templateGlob)
}

// TestSiteScriptsNeverBuildMarkupFromData: the map draws titles, places and
// names that editors typed, and the popup is the one place a script puts them
// on a page.
func TestSiteScriptsNeverBuildMarkupFromData(t *testing.T) {
	webcheck.ScriptsNeverBuildMarkupFromData(t, assets, "static")
}
