package console

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

// TestEveryKeyTheConsoleAsksForIsDefined, including the ones a template builds
// from a value: the name of every status, kind and level a form offers.
//
// Those are listed from the same slices the forms are drawn from, so adding a
// choice to a form without naming it in the catalogue fails here rather than
// showing an editor `kind.merger` in a dropdown.
func TestEveryKeyTheConsoleAsksForIsDefined(t *testing.T) {
	var enumerated []string
	for prefix, values := range map[string][]string{
		"status":        statuses,
		"kind":          topicKinds,
		"level":         levels,
		"action_kind":   actionKinds,
		"member_kind":   memberKinds,
		"change_kind":   {"create", "update", "delete"},
		"change_status": {"pending", "applied", "rejected", "withdrawn", "failed"},
	} {
		for _, value := range values {
			enumerated = append(enumerated, prefix+"."+value)
		}
	}
	for _, key := range noticeKeys {
		enumerated = append(enumerated, key)
	}
	// What the audit log may hold: every verb the backend writes and every
	// kind of thing it writes one about.
	for _, verb := range []string{"create", "update", "delete", "publish", "archive", "cancel", "config_change",
		"propose", "approve", "reject", "withdraw"} {
		enumerated = append(enumerated, "audit.action."+verb)
	}
	for _, subject := range []string{"collective", "member", "organisation", "topic", "update", "action", "installation"} {
		enumerated = append(enumerated, "audit.type."+subject)
	}

	webcheck.EveryKeyAskedForIsDefined(t, assets, localesDir, templateGlob, enumerated)
}

func TestEveryInlineScriptCarriesTheNonce(t *testing.T) {
	webcheck.EveryInlineScriptCarriesTheNonce(t, assets, templateGlob)
}

func TestNoPageLoadsForeignResources(t *testing.T) {
	webcheck.NoPageLoadsForeignResources(t, assets, templateGlob)
}

func TestNoInlineHandlersOrStyles(t *testing.T) {
	webcheck.NoInlineHandlersOrStyles(t, assets, templateGlob)
}

// TestConsoleScriptsNeverBuildMarkupFromData matters more here than anywhere:
// a stored script in the console would run as an editor, and an editor can
// publish.
func TestConsoleScriptsNeverBuildMarkupFromData(t *testing.T) {
	webcheck.ScriptsNeverBuildMarkupFromData(t, assets, "static")
}
