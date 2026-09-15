package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Basa-Futura/basa-cli/internal/config"
)

// dealJSON builds one deal in the API's shape. The project id is derived from
// its name so two deals naming the same project share an id, as real ones do.
func dealJSON(id, statusSlug, statusLabel, project, brand, counterparty, updated string) string {
	return fmt.Sprintf(`{"id":%q,"stage":{"slug":"outreach","label":"Outreach"},`+
		`"status":{"slug":%q,"label":%q},"project":{"id":"%s-id","name":%q},"brand":{"name":%q},`+
		`"role":{"name":"Nano Outreach"},"counterparty":{"name":%q},"assigned_to":"Dana Reed",`+
		`"sent_at":null,"updated_at":%q}`,
		id, statusSlug, statusLabel, strings.ReplaceAll(project, " ", "-"), project, brand, counterparty, updated)
}

func dealsPage(deals []string, current, last, total int) string {
	return fmt.Sprintf(`{"data":[%s],"meta":{"current_page":%d,"last_page":%d,"per_page":100,"total":%d}}`,
		strings.Join(deals, ","), current, last, total)
}

// Forty-seven rows repeating one project name and one brand is noise. When the
// whole page is one project, it is said once in the heading and the two columns
// go — and come back the moment a second project appears.
func TestDealsListSaysAUniformProjectOnceInTheHeading(t *testing.T) {
	body := dealsPage([]string{
		dealJSON("aaa", "completed", "Completed", "Spring Campaign", "Acme", "Sam Rivera", "2026-06-01T10:00:00+00:00"),
		dealJSON("bbb", "completed", "Completed", "Spring Campaign", "Acme", "Jordan Lee", "2026-06-02T10:00:00+00:00"),
	}, 1, 1, 2)
	h := newHarness(t, apiFor(meWith("Acme Agency"), body, nil))
	t.Setenv(config.EnvVarToken, "42|t")

	stdout, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "local · Acme Agency · Spring Campaign (Acme)") {
		t.Errorf("heading should name the one project and brand, got:\n%s", stderr)
	}
	if strings.Contains(stdout, "PROJECT") || strings.Contains(stdout, "Spring Campaign") {
		t.Errorf("a uniform project must not also be a column:\n%s", stdout)
	}
}

func TestDealsListKeepsProjectColumnsWhenProjectsDiffer(t *testing.T) {
	body := dealsPage([]string{
		dealJSON("aaa", "completed", "Completed", "Spring Campaign", "Acme", "Sam Rivera", "2026-06-01T10:00:00+00:00"),
		dealJSON("bbb", "completed", "Completed", "Autumn Push", "Northwind", "Jordan Lee", "2026-06-02T10:00:00+00:00"),
	}, 1, 1, 2)
	h := newHarness(t, apiFor(meWith("Acme Agency"), body, nil))
	t.Setenv(config.EnvVarToken, "42|t")

	stdout, stderr, _ := h.run("deals", "list", "--env", "local")

	for _, want := range []string{"PROJECT", "BRAND", "Spring Campaign", "Autumn Push", "Northwind"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table should carry %q when projects differ:\n%s", want, stdout)
		}
	}
	if strings.Contains(stderr, "Spring Campaign") {
		t.Errorf("heading must not pick one project of several:\n%s", stderr)
	}
}

// Rows sharing a status sit together, the group touched most recently first,
// server order within a group. That is presentation: the client does not know
// what "On Hold" means, only when it last moved. And the tally says the shape
// of the list before the rows do, in the same order as the table.
func TestDealsListGroupsByStatusMostRecentGroupFirstAndTallies(t *testing.T) {
	body := dealsPage([]string{
		dealJSON("aaa", "inactive", "On Hold", "Spring Campaign", "Acme", "A", "2026-05-29T18:44:39+00:00"),
		dealJSON("bbb", "outreach_assigned", "Ready to Send", "Spring Campaign", "Acme", "B", "2026-07-14T18:36:52+00:00"),
		dealJSON("ccc", "inactive", "On Hold", "Spring Campaign", "Acme", "C", "2026-05-28T09:00:00+00:00"),
		dealJSON("ddd", "completed", "Completed", "Spring Campaign", "Acme", "D", "2026-06-01T10:00:00+00:00"),
	}, 1, 1, 4)
	h := newHarness(t, apiFor(meWith("Acme Agency"), body, nil))
	t.Setenv(config.EnvVarToken, "42|t")

	stdout, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
	order := []string{"bbb", "ddd", "aaa", "ccc"}
	last := -1
	for _, id := range order {
		i := strings.Index(stdout, id)
		if i < 0 {
			t.Fatalf("%s missing from table:\n%s", id, stdout)
		}
		if i < last {
			t.Errorf("expected row order %v, got:\n%s", order, stdout)
			break
		}
		last = i
	}
	if want := "4 deals · 1 Ready to Send · 1 Completed · 2 On Hold"; !strings.Contains(stderr, want) {
		t.Errorf("tally should read %q, got:\n%s", want, stderr)
	}
}

// pagedAPI serves a two-page listing and records what each request asked for.
func pagedAPI(pages *[]string, perPage *[]string) http.HandlerFunc {
	page1 := dealsPage([]string{
		dealJSON("p1a", "completed", "Completed", "Spring Campaign", "Acme", "A", "2026-06-01T10:00:00+00:00"),
		strings.Replace(dealJSON("p1b", "completed", "Completed", "Spring Campaign", "Acme", "B", "2026-06-01T10:00:00+00:00"),
			`"sent_at":null`, `"sent_at":null,"extra":"kept"`, 1),
	}, 1, 2, 3)
	page2 := dealsPage([]string{
		dealJSON("p2a", "completed", "Completed", "Spring Campaign", "Acme", "C", "2026-06-01T10:00:00+00:00"),
	}, 2, 2, 3)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/me") {
			_, _ = w.Write([]byte(meWith("Acme Agency")))
			return
		}
		q := r.URL.Query()
		*pages = append(*pages, q.Get("page"))
		*perPage = append(*perPage, q.Get("per_page"))
		if q.Get("page") == "2" {
			_, _ = w.Write([]byte(page2))
			return
		}
		_, _ = w.Write([]byte(page1))
	}
}

// --all walks every page at the server's largest page size, so a team with
// many deals costs as few requests as possible against the rate limit, and
// nothing is left to warn about.
func TestDealsListAllWalksEveryPage(t *testing.T) {
	var pages, perPage []string
	h := newHarness(t, pagedAPI(&pages, &perPage))
	t.Setenv(config.EnvVarToken, "42|t")

	stdout, stderr, code := h.run("deals", "list", "--env", "local", "--all")

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
	for _, id := range []string{"p1a", "p1b", "p2a"} {
		if !strings.Contains(stdout, id) {
			t.Errorf("%s missing:\n%s", id, stdout)
		}
	}
	if got := strings.Join(pages, ","); got != "1,2" {
		t.Errorf("pages requested = %q, want 1,2", got)
	}
	for _, pp := range perPage {
		if pp != "100" {
			t.Errorf("per_page = %q, want 100 (the server's maximum)", pp)
		}
	}
	if strings.Contains(stderr, "Showing") {
		t.Errorf("--all has nothing left to warn about:\n%s", stderr)
	}
}

// --all --json splices the pages into one envelope with each item exactly as
// the server sent it — a field this version knows nothing about survives.
func TestDealsListAllJSONSplicesPagesVerbatim(t *testing.T) {
	var pages, perPage []string
	h := newHarness(t, pagedAPI(&pages, &perPage))
	t.Setenv(config.EnvVarToken, "42|t")

	stdout, stderr, code := h.run("deals", "list", "--env", "local", "--all", "--json")

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
	var doc struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Total    int `json:"total"`
			LastPage int `json:"last_page"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(doc.Data) != 3 || doc.Meta.Total != 3 || doc.Meta.LastPage != 1 {
		t.Errorf("want 3 items, total 3, one page; got %d items, total %d, last_page %d", len(doc.Data), doc.Meta.Total, doc.Meta.LastPage)
	}
	// The encoder re-indents, so byte-compare the *content*, not the bytes: an
	// unknown field must come through with its value intact.
	var second map[string]any
	if err := json.Unmarshal(doc.Data[1], &second); err != nil {
		t.Fatalf("second item is not an object: %v", err)
	}
	if second["extra"] != "kept" {
		t.Errorf("items must be spliced as the server sent them; the unknown field was lost:\n%s", stdout)
	}
}

func TestDealsListAllAndLimitIsAUsageError(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|t")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--all", "--limit", "5")

	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "--all") || !strings.Contains(stderr, "--limit") {
		t.Errorf("should name both flags:\n%s", stderr)
	}
}

// Ordering must survive fractional seconds. Go's time.Parse accepts a
// fractional second immediately after the seconds field even when the layout
// omits it, so RFC3339 is enough — this pins it rather than leaving the next
// reader to re-derive it. The API sends whole seconds today; a server that
// started sending tenths would not silently flatten this ordering to zero
// times.
func TestDealsListOrdersCorrectlyWithFractionalSecondTimestamps(t *testing.T) {
	body := dealsPage([]string{
		dealJSON("older", "inactive", "On Hold", "Spring Campaign", "Acme", "A", "2026-05-29T18:44:39.123456+00:00"),
		dealJSON("newer", "outreach_assigned", "Ready to Send", "Spring Campaign", "Acme", "B", "2026-07-14T18:36:52.5+00:00"),
	}, 1, 1, 2)
	h := newHarness(t, apiFor(meWith("Acme Agency"), body, nil))
	t.Setenv(config.EnvVarToken, "42|t")

	stdout, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
	newer, older := strings.Index(stdout, "newer"), strings.Index(stdout, "older")
	if newer < 0 || older < 0 {
		t.Fatalf("both rows should render:\n%s", stdout)
	}
	if newer > older {
		t.Errorf("the group touched in July must precede the one touched in May:\n%s", stdout)
	}
}

// The zero case needs its own test. --all with --limit 0 rejects only because
// the guard tests the pointer for nil rather than the value it points at; a
// regression to checking the value would let zero slip through while every
// other test here still passed. Also asserts the refusal happens before any
// request: the conflict is decidable from the flags alone, so an operator who
// mistyped this never touches the server.
func TestDealsListAllAndAnExplicitZeroLimitIsAlsoAUsageError(t *testing.T) {
	requests := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meWith("Acme Agency")))
	})
	t.Setenv(config.EnvVarToken, "42|t")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--all", "--limit", "0")

	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "--all") || !strings.Contains(stderr, "--limit") {
		t.Errorf("should name both flags:\n%s", stderr)
	}
	if requests != 0 {
		t.Errorf("a flag conflict should be decided before any request, got %d", requests)
	}
}
