package console

import (
	"net/http"
	"strconv"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// auditPerPage is how much of the log one page shows.
const auditPerPage = 50

// auditPage reads the log of what was changed through the console.
type auditPage struct {
	page

	Entries []apiclient.AuditEntry
	Total   int64

	// Page is the one being shown, counted from one, and Pages how many
	// there are.
	Page  int
	Pages int
}

func (c *console) audit(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || number < 1 {
		number = 1
	}

	entries, total, err := c.staff(r).Audit(r.Context(), auditPerPage, (number-1)*auditPerPage)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := auditPage{page: c.newPage(r, "audit.title")}
	data.Entries = entries
	data.Total = total
	data.Page = number
	data.Pages = int((total + auditPerPage - 1) / auditPerPage)
	c.renderer.Render(w, http.StatusOK, "audit", data)
}
