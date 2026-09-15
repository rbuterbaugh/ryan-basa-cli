package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Basa-Futura/basa-cli/internal/config"
)

// meWith builds a /me body advertising the given teams.
func meWith(teams ...string) string {
	entries := make([]string, 0, len(teams))
	for i, name := range teams {
		entries = append(entries, fmt.Sprintf(`{"id":%d,"name":%q,"personal":false}`, i+7, name))
	}

	return `{"data":{"id":42,"name":"Dana Reed","email":"dana@example.test",
	  "teams":[` + strings.Join(entries, ",") + `],
	  "token":{"name":"basa-cli","abilities":["read"],"expires_at":null}}}`
}

const dealsBody = `{"data":[
  {"id":"K3mQz","stage":{"slug":"contracting","label":"Contracting"},
   "status":{"slug":"awaiting_signature","label":"Awaiting signature"},
   "project":{"id":"9f2c1111-2222-3333-4444-555566667777","name":"Spring Campaign"},
   "brand":{"name":"Acme"},"role":{"name":"Lead Creator"},
   "counterparty":{"name":"Jordan Lee"},"assigned_to":"Dana Reed",
   "sent_at":"2026-08-01T12:00:00+00:00","updated_at":"2026-08-18T09:30:00+00:00"}
],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":1}}`

// apiFor routes /me and the deals endpoints, and records the last deals query
// so tests can assert that filters reached the server.
func apiFor(me, deals string, lastQuery *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/me"):
			_, _ = w.Write([]byte(me))
		case strings.Contains(r.URL.Path, "/deals"):
			if lastQuery != nil {
				*lastQuery = r.URL.RawQuery
			}
			_, _ = w.Write([]byte(deals))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no route"}`))
		}
	}
}

// --- listing ---------------------------------------------------------------

func TestDealsListRendersATable(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	// STATUS is not a synonym for STAGE. The fixture deliberately gives the deal
	// a coarse stage of "Contracting" and a status of "Awaiting signature", so a
	// table that dropped one of them would fail here rather than look plausible.
	for _, want := range []string{
		"ID", "STAGE", "STATUS", "K3mQz", "Contracting", "Awaiting signature",
		"Jordan Lee", "Lead Creator", "Dana Reed",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table is missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
	// One deal is one project, so the project and brand are said once, in the
	// heading on stderr, and are not columns — see the readability tests.
	if !strings.Contains(stderr, "Spring Campaign (Acme)") {
		t.Errorf("heading should name the one project and brand, got:\n%s", stderr)
	}
	// The date is trimmed to the day — a COO scanning a list wants the day.
	if !strings.Contains(stdout, "2026-08-18") || strings.Contains(stdout, "09:30:00") {
		t.Errorf("expected a trimmed date, got:\n%s", stdout)
	}
}

// Which environment and team the operator is looking at must always be visible,
// and must be on stderr so it cannot corrupt a pipeline.
func TestDealsListNamesTheEnvironmentAndTeamOnStderr(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, _ := h.run("deals", "list", "--env", "local")

	if !strings.Contains(stderr, "local") || !strings.Contains(stderr, "Acme Agency") {
		t.Errorf("stderr should name the environment and team, got:\n%s", stderr)
	}
	// The whole context line, not a fragment. Asserting on "Acme" alone would
	// also match the table's own cells; an earlier version of this check ANDed
	// two predicates that cannot both hold ("contains Acme Agency" implies
	// "contains Acme"), so it could never fail and guarded nothing.
	if strings.Contains(stdout, "local \u00b7 Acme Agency") {
		t.Errorf("the context line must not be on stdout:\n%s", stdout)
	}
}

// A stray positional argument used to be ignored, so `basa deals list <id>`
// listed every deal and exited 0 — which reads as "that id matched all of
// these". It is almost always a typo for `show`, and the error says so.
func TestDealsListRejectsAStrayArgument(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("deals", "list", "EfhxL", "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("nothing should reach stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "EfhxL") || !strings.Contains(stderr, "deals show") {
		t.Errorf("the error should quote the argument and point at show, got:\n%s", stderr)
	}
}

// `show` takes one id. A second is a mistake worth reporting, not something to
// silently drop.
func TestDealsShowRejectsASecondArgument(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("deals", "show", "EfhxL", "gbHJd", "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// A bare `deals show` keeps its own question rather than Cobra's wording.
func TestDealsShowWithoutAnIDAsksForOne(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "show", "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "Which deal?") {
		t.Errorf("expected the friendly prompt, got:\n%s", stderr)
	}
}

// An out-of-range --limit must reach the server, so the operator sees its own
// 1-100 validation message. Silently dropping it returned a default-sized page
// and looked like the request had been honoured.
func TestDealsListForwardsAnOutOfRangeLimit(t *testing.T) {
	var query string
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("deals", "list", "--limit", "-1", "--env", "local")

	if !strings.Contains(query, "per_page=-1") {
		t.Errorf("per_page=-1 should reach the server, got query %q", query)
	}
}

// An unset --limit still sends nothing, leaving the page size to the server.
func TestDealsListOmitsAnUnsetLimit(t *testing.T) {
	var query string
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("deals", "list", "--env", "local")

	if strings.Contains(query, "per_page") {
		t.Errorf("no per_page should be sent when --limit is unset, got query %q", query)
	}
}

func TestDealsListJSONEmitsTheAPIShape(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("deals", "list", "--env", "local", "--json")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON:\n%s", stdout)
	}

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "K3mQz" {
		t.Fatalf("unexpected data: %+v", payload.Data)
	}
	// The paginator metadata must survive, not be flattened away.
	if payload.Meta.Total != 1 {
		t.Errorf("meta.total did not survive: %+v", payload.Meta)
	}
	// A project id is a UUID, not a Sqid.
	if !strings.Contains(payload.Data[0].Project.ID, "-") {
		t.Errorf("project id should be a uuid, got %q", payload.Data[0].Project.ID)
	}
}

func TestDealsListPassesFiltersToTheServer(t *testing.T) {
	var query string
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("deals", "list", "--env", "local", "--stage", "contracting", "--limit", "5")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(query, "stage=contracting") {
		t.Errorf("stage filter did not reach the server, query was %q", query)
	}
	if !strings.Contains(query, "per_page=5") {
		t.Errorf("--limit did not become per_page, query was %q", query)
	}
}

func TestDealsListSaysSoWhenEmpty(t *testing.T) {
	empty := `{"data":[],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":0}}`
	h := newHarness(t, apiFor(meWith("Acme Agency"), empty, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0 — an empty result is not an error", code)
	}
	if !strings.Contains(strings.ToLower(stderr), "no deals") {
		t.Errorf("should say nothing matched, got:\n%s", stderr)
	}
}

// Silently truncating reads as "this is everything", which for a status report
// is worse than showing nothing.
func TestDealsListWarnsWhenTruncated(t *testing.T) {
	truncated := strings.Replace(dealsBody, `"total":1`, `"total":40`, 1)
	h := newHarness(t, apiFor(meWith("Acme Agency"), truncated, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, _ := h.run("deals", "list", "--env", "local")

	if !strings.Contains(stderr, "of 40") {
		t.Errorf("should warn that more exist, got:\n%s", stderr)
	}
}

// --- show ------------------------------------------------------------------

func TestDealsShowRendersARecord(t *testing.T) {
	single := `{"data":` + strings.TrimSuffix(strings.TrimPrefix(dealsBody, `{"data":[`), `
],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":1}}`) + `}`

	h := newHarness(t, apiFor(meWith("Acme Agency"), single, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("deals", "show", "K3mQz", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"Environment", "Team", "Deal", "K3mQz", "Contracting", "Jordan Lee"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("record is missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
}

func TestDealsShowRequiresAnId(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "show", "--env", "local")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "Which deal") {
		t.Errorf("got:\n%s", stderr)
	}
}

// --- team resolution -------------------------------------------------------

func TestASingleTeamIsResolvedWithoutAFlag(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
}

// With several teams the CLI must not pick one. Unlike --env this is resolved
// implicitly when unambiguous, but ambiguity is never guessed away.
func TestSeveralTeamsWithoutAFlagIsAUsageError(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency", "Beta Co"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, want := range []string{"--team", "Acme Agency", "Beta Co"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("error should mention %q, got:\n%s", want, stderr)
		}
	}
}

func TestTeamCanBeChosenByName(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency", "Beta Co"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--team", "Beta Co")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Beta Co") {
		t.Errorf("should report the chosen team, got:\n%s", stderr)
	}
}

func TestTeamCanBeChosenById(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency", "Beta Co"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	// meWith numbers teams from 7, so "Beta Co" is 8.
	_, stderr, code := h.run("deals", "list", "--env", "local", "--team", "8")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Beta Co") {
		t.Errorf("id 8 should resolve to Beta Co, got:\n%s", stderr)
	}
}

func TestTeamCanBeChosenByUniqueFragment(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency", "Beta Co"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--team", "beta")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Beta Co") {
		t.Errorf("got:\n%s", stderr)
	}
}

// An ambiguous fragment must be an error, never a coin flip — silently picking
// one would show the wrong team's data under a plausible-looking heading.
func TestAnAmbiguousTeamFragmentIsRefused(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme North", "Acme South"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--team", "acme")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "more than one") {
		t.Errorf("got:\n%s", stderr)
	}
	for _, want := range []string{"Acme North", "Acme South"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("should list the candidates, missing %q:\n%s", want, stderr)
		}
	}
}

// An exact name must win over a substring, so "Acme" resolves cleanly even
// though "Acme Agency" also contains it.
func TestAnExactTeamNameBeatsASubstring(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme", "Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--team", "Acme")

	if code != 0 {
		t.Fatalf("exit %d, want 0 — an exact name should not be ambiguous. stderr:\n%s", code, stderr)
	}
}

func TestAnUnknownTeamListsYours(t *testing.T) {
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local", "--team", "Nope Ltd")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "Acme Agency") {
		t.Errorf("should list your teams, got:\n%s", stderr)
	}
}

func TestNoTeamsAtAllIsExplained(t *testing.T) {
	h := newHarness(t, apiFor(meWith(), dealsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "not a member of any team") {
		t.Errorf("got:\n%s", stderr)
	}
}

// --- failures propagate ----------------------------------------------------

func TestDealsListPropagatesForbidden(t *testing.T) {
	h := newHarness(t, status(http.StatusForbidden, `{"message":"API access is not enabled for this account."}`))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if !strings.Contains(stderr, "API access is not enabled") {
		t.Errorf("got:\n%s", stderr)
	}
}

func TestDealsShowPropagatesNotFound(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/me") {
			_, _ = w.Write([]byte(meWith("Acme Agency")))

			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not found."}`))
	})
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("deals", "show", "nope", "--env", "local")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// A seller-only account gets 403 from every team-scoped endpoint: sellers reach
// Basa through the invitation links mailed to them, not through tokens.
//
// The generic 403 path is already covered in root_test.go. This pins the part
// that is specific to sellers and easy to regress: the fallback hint tells the
// operator to "ask a Basa administrator to enable API access", which is the
// right advice for a missing feature flag and the WRONG advice here — no
// administrator action makes a seller account into a buyer one. So the server's
// own hint has to win, and the fallback must not appear.
func TestDealsListSurfacesTheSellerHintRatherThanTheAdminFallback(t *testing.T) {
	h := newHarness(t, status(http.StatusForbidden,
		`{"message":"These endpoints are available to buyer accounts.",`+
			`"hint":"Seller access is through the invitation links sent to you, not the API."}`))
	t.Setenv(config.EnvVarToken, "42|sellertoken")

	_, stderr, code := h.run("deals", "list", "--env", "local")

	if code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if !strings.Contains(stderr, "buyer accounts") {
		t.Errorf("should surface the server's message, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "invitation links") {
		t.Errorf("should surface the server's hint, got:\n%s", stderr)
	}
	if strings.Contains(stderr, "administrator") {
		t.Errorf("the admin fallback must not appear for a seller; it is wrong advice:\n%s", stderr)
	}
}

// `--limit 0` is not the same request as omitting --limit. It is a page size
// the server rejects with its own 1-100 message, and folding it into the
// default returned a full page and reported it as success -- the defect that
// survived the --archived fix because Limit was still a plain int.
func TestDealsListForwardsAnExplicitZeroLimit(t *testing.T) {
	var query string
	h := newHarness(t, apiFor(meWith("Acme Agency"), dealsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("deals", "list", "--limit", "0", "--env", "local")

	if !strings.Contains(query, "per_page=0") {
		t.Errorf("per_page=0 should reach the server, got query %q", query)
	}
}
