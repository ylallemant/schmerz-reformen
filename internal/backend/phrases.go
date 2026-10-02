package backend

// phrases are the words the backend itself puts in a notification.
//
// There are two of them, and that is the design rather than a shortfall.
// Everything else in a notification is content somebody wrote — the name of a
// collective, the title of an update — and travels as it is. These are the
// only places the *backend* has to say something in its own voice, to a lock
// screen, in a language it cannot ask the reader for because an account knows
// nothing about its owner.
//
// So they live here, in one language per installation, rather than in a
// catalogue the backend would otherwise need for two strings.
type phrases struct {
	// Cancelled and Moved take the action's title.
	Cancelled string
	Moved     string
}

var phrasebook = map[string]phrases{
	"de": {Cancelled: "Abgesagt: %s", Moved: "Geändert: %s"},
	"en": {Cancelled: "Cancelled: %s", Moved: "Changed: %s"},
}

// phrasesFor returns the phrases for a language, falling back to German: a
// notification in the wrong language still says which action it is about, and
// one that failed to be written says nothing.
func phrasesFor(language string) phrases {
	if found, ok := phrasebook[language]; ok {
		return found
	}
	return phrasebook["de"]
}
