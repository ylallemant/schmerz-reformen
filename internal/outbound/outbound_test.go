package outbound

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTheUserAgentNamesTheSiteAndWhoToWrite(t *testing.T) {
	for _, tc := range []struct{ version, project, contact, want string }{
		{"1.2.0", "https://github.com/ylallemant/schmerz-reformen", "buero@example.org",
			"schmerz-reformen/1.2.0 (+https://github.com/ylallemant/schmerz-reformen; buero@example.org)"},
		{"1.2.0", "", "buero@example.org", "schmerz-reformen/1.2.0 (buero@example.org)"},
		{"", "", " ", "schmerz-reformen/dev"},
	} {
		if got := UserAgent(tc.version, tc.project, tc.contact); got != tc.want {
			t.Errorf("UserAgent(%q, %q, %q) = %q, want %q", tc.version, tc.project, tc.contact, got, tc.want)
		}
	}
}

// TestEveryRequestCarriesIt — and one that names its own keeps it.
func TestEveryRequestCarriesIt(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("User-Agent"))
	}))
	t.Cleanup(server.Close)
	client := Client(5*time.Second, "schmerz-reformen/test (a@example.org)")

	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	if _, err := client.Do(request); err != nil {
		t.Fatal(err)
	}
	own, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	own.Header.Set("User-Agent", "its-own")
	if _, err := client.Do(own); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "schmerz-reformen/test (a@example.org)" || seen[1] != "its-own" {
		t.Errorf("seen %q", seen)
	}
	if request.Header.Get("User-Agent") != "" {
		t.Error("the caller's request was changed")
	}
}
