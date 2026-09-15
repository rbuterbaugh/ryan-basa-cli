package cli_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Basa-Futura/basa-cli/internal/config"
)

// Project ids are UUIDs, not Sqids, and the full value is what
// `deals list --project` takes — so the fixtures use real-length ones.
const (
	autumnID = "9f2c1111-2222-3333-4444-555566667777"
	springID = "9f2c8888-9999-aaaa-bbbb-ccccddddeeee"
)

// Two projects with different brands, both active: the shape where every
// column earns its place.
const projectsBody = `{"data":[
  {"id":"` + autumnID + `","name":"Autumn Launch",
   "external_name":"Autumn with Northwind","brand":{"name":"Northwind Trading"},
   "archived":false,"type":"social","nda_required":true,
   "created_at":"2026-08-01T12:00:00+00:00","updated_at":"2026-08-19T09:30:00+00:00"},
  {"id":"` + springID + `","name":"Spring Campaign",
   "external_name":null,"brand":{"name":"Acme"},
   "archived":false,"type":"social","nda_required":false,
   "created_at":"2026-07-01T12:00:00+00:00","updated_at":"2026-08-17T09:30:00+00:00"}
],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":2}}`

// projectsAPI routes /me and the projects endpoints, recording the last query
// so tests can assert filters reached the server.
func projectsAPI(me, projects string, lastQuery *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/me"):
			_, _ = w.Write([]byte(me))
		case strings.Contains(r.URL.Path, "/projects"):
			if lastQuery != nil {
				*lastQuery = r.URL.RawQuery
			}
			_, _ = w.Write([]byte(projects))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no route"}`))
		}
	}
}

func TestProjectsListRendersATable(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"ID", "NAME", "BRAND", "TYPE", "NDA", "UPDATED",
		"Autumn Launch", "Northwind Trading", "social", "2026-08-19"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table is missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
}

// The whole id, not a prefix of it: this is the value that goes into
// `deals list --project`, and a truncated one is useless.
func TestProjectsListShowsTheFullUUID(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, autumnID) {
		t.Errorf("the full project id should be in the table, got:\n%s", stdout)
	}
}

// nda_required false is a known answer, not missing data, so it must read as
// "no" rather than the em dash an absent field gets.
func TestProjectsListRendersFalseAsNoNotADash(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	// Scoped to each row, so this cannot pass on the word appearing anywhere
	// else in the table.
	autumn, spring := rowFor(t, stdout, autumnID), rowFor(t, stdout, springID)
	if !strings.Contains(autumn, "yes") {
		t.Errorf("nda_required true should read yes, row was:\n%s", autumn)
	}
	if !strings.Contains(spring, "no") {
		t.Errorf("nda_required false should read no, not a dash, row was:\n%s", spring)
	}
	if strings.Contains(spring, "—") {
		t.Errorf("a known false must not render as an em dash, row was:\n%s", spring)
	}
}

// Same lesson as the deals list: a column repeating one value on every row is
// noise. It is said once in the heading instead.
func TestProjectsListCollapsesAUniformBrandIntoTheHeading(t *testing.T) {
	oneBrand := strings.Replace(projectsBody, `"brand":{"name":"Acme"}`, `"brand":{"name":"Northwind Trading"}`, 1)
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), oneBrand, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Northwind Trading") {
		t.Errorf("the one brand should be named in the heading, got stderr:\n%s", stderr)
	}
	if strings.Contains(stdout, "BRAND") {
		t.Errorf("the BRAND column should be gone when every row shares it, got:\n%s", stdout)
	}
}

// And it comes back the moment a second brand appears.
func TestProjectsListKeepsTheBrandColumnWhenBrandsDiffer(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "BRAND") {
		t.Errorf("two brands should keep the column, got:\n%s", stdout)
	}
	if strings.Contains(stderr, "Northwind Trading") {
		t.Errorf("the heading must not claim one brand when there are two, got:\n%s", stderr)
	}
}

// Active is the default listing, so an ARCHIVED column reading "no" on every
// row would be noise.
func TestProjectsListHidesTheArchivedColumnWhenAllAreActive(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if strings.Contains(stdout, "ARCHIVED") {
		t.Errorf("no ARCHIVED column when every row is active, got:\n%s", stdout)
	}
}

func TestProjectsListShowsTheArchivedColumnWhenThePageIsMixed(t *testing.T) {
	mixed := strings.Replace(projectsBody, `"archived":false,"type":"social","nda_required":false`,
		`"archived":true,"type":"social","nda_required":false`, 1)
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), mixed, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("projects", "list", "--env", "local", "--archived", "all")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "ARCHIVED") {
		t.Errorf("a mixed page needs the column, got:\n%s", stdout)
	}
}

func TestProjectsListSaysArchivedInTheHeadingWhenEveryRowIs(t *testing.T) {
	allArchived := strings.ReplaceAll(projectsBody, `"archived":false`, `"archived":true`)
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), allArchived, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("projects", "list", "--env", "local", "--archived", "true")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stderr, "archived") {
		t.Errorf("the heading should say the whole page is archived, got:\n%s", stderr)
	}
	if strings.Contains(stdout, "ARCHIVED") {
		t.Errorf("no column needed when the heading already said it, got:\n%s", stdout)
	}
}

func TestProjectsListPassesItsFilters(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("projects", "list", "--env", "local",
		"--archived", "all", "--search", "northwind", "--limit", "3")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{"archived=all", "search=northwind", "per_page=3"} {
		if !strings.Contains(query, want) {
			t.Errorf("%q did not reach the server, query was %q", want, query)
		}
	}
}

// The tri-state is the server's, and so is the message for anything outside
// it. The client must forward rather than pre-judge, or the operator gets a
// client-invented error naming values it guessed at.
func TestProjectsListForwardsAnUnknownArchivedValue(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("projects", "list", "--env", "local", "--archived", "maybe")

	if !strings.Contains(query, "archived=maybe") {
		t.Errorf("archived=maybe should reach the server, got query %q", query)
	}
}

// `--archived=` is not the same request as omitting --archived. A shell writes
// the empty form whenever an interpolated variable is unset, and the server
// answers it with a 422 naming the valid values -- so dropping it here would
// return an active-only page and report it as success. Copilot caught this on
// PR #1; it is the same defect the --limit forwarding comment describes.
func TestProjectsListForwardsAnExplicitlyEmptyArchived(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("projects", "list", "--env", "local", "--archived", "")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	parsed, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	values, present := parsed["archived"]
	if !present {
		t.Fatalf("archived should still be sent when given an empty value, query was %q", query)
	}
	if len(values) != 1 || values[0] != "" {
		t.Errorf("archived should be sent empty, got %#v", values)
	}
}

// And omitting the flag must NOT send the parameter, or every default listing
// would carry one the operator never asked for.
func TestProjectsListOmitsArchivedWhenTheFlagIsAbsent(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	parsed, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	if _, present := parsed["archived"]; present {
		t.Errorf("archived should be absent when the flag is not given, query was %q", query)
	}
}

func TestProjectsListSurfacesTheServersArchivedMessage(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/me") {
			_, _ = w.Write([]byte(meWith("Acme Agency")))

			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"archived must be one of: true, false, all."}`))
	})
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("projects", "list", "--env", "local", "--archived", "maybe")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "must be one of") {
		t.Errorf("should surface the server's own message, got:\n%s", stderr)
	}
}

func TestProjectsListForwardsAnOutOfRangeLimit(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("projects", "list", "--limit", "-1", "--env", "local")

	if !strings.Contains(query, "per_page=-1") {
		t.Errorf("per_page=-1 should reach the server, got query %q", query)
	}
}

func TestProjectsListJSONEmitsTheAPIShape(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("projects", "list", "--env", "local", "--json")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON:\n%s", stdout)
	}

	var payload struct {
		Data []struct {
			ID           string  `json:"id"`
			ExternalName *string `json:"external_name"`
			NDARequired  bool    `json:"nda_required"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(payload.Data))
	}
	if payload.Data[0].ID != autumnID {
		t.Errorf("the id must survive unchanged, got %q", payload.Data[0].ID)
	}
	// A null external_name must stay null, not be invented into "".
	if payload.Data[1].ExternalName != nil {
		t.Errorf("null external_name should survive as null, got %q", *payload.Data[1].ExternalName)
	}
	if payload.Meta.Total != 2 {
		t.Errorf("meta.total did not survive: %d", payload.Meta.Total)
	}
}

// Same defect the deals and contracts commands had: a stray positional was
// ignored, so `basa projects list <id>` returned the whole list and exited 0.
func TestProjectsListRejectsAStrayArgument(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("projects", "list", autumnID, "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("nothing should reach stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "projects show") {
		t.Errorf("the error should point at show, got:\n%s", stderr)
	}
}

func TestProjectsListSaysSoWhenEmpty(t *testing.T) {
	empty := `{"data":[],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":0}}`
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), empty, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("projects", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0 — an empty result is not an error", code)
	}
	if !strings.Contains(strings.ToLower(stderr), "no projects") {
		t.Errorf("should say nothing matched, got:\n%s", stderr)
	}
}

func TestProjectsListWarnsWhenTruncated(t *testing.T) {
	truncated := strings.Replace(projectsBody, `"total":2`, `"total":31`, 1)
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), truncated, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, _ := h.run("projects", "list", "--env", "local")

	if !strings.Contains(stderr, "of 31") {
		t.Errorf("should warn that more exist, got:\n%s", stderr)
	}
}

func TestProjectsShowRendersARecord(t *testing.T) {
	single := `{"data":{"id":"` + autumnID + `","name":"Autumn Launch",
	  "external_name":"Autumn with Northwind","brand":{"name":"Northwind Trading"},
	  "archived":false,"type":"social","nda_required":true,
	  "created_at":"2026-08-01T12:00:00+00:00","updated_at":"2026-08-19T09:30:00+00:00"}}`

	h := newHarness(t, projectsAPI(meWith("Acme Agency"), single, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("projects", "show", autumnID, "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"Project", autumnID, "Autumn Launch", "Autumn with Northwind",
		"Northwind Trading", "NDA required", "2026-08-01"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("record is missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "<nil>") {
		t.Errorf("a null field leaked as <nil>:\n%s", stdout)
	}
}

// A project with no external name is the common case, and talent then sees the
// internal one. That is a fact about the project, not a gap in the response.
func TestProjectsShowSaysNotSetForAnAbsentExternalName(t *testing.T) {
	single := `{"data":{"id":"` + springID + `","name":"Spring Campaign",
	  "external_name":null,"brand":null,"archived":false,"type":null,
	  "nda_required":false,"created_at":null,"updated_at":null}}`

	h := newHarness(t, projectsAPI(meWith("Acme Agency"), single, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("projects", "show", springID, "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "not set") {
		t.Errorf("an absent external name should read as not set, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "<nil>") {
		t.Errorf("a null field leaked as <nil>:\n%s", stdout)
	}
}

func TestProjectsShowRequiresAnId(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("projects", "show", "--env", "local")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "Which project") {
		t.Errorf("got:\n%s", stderr)
	}
}

func TestProjectsShowRejectsASecondArgument(t *testing.T) {
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("projects", "show", autumnID, springID, "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// An id that cannot name a project is a 404 by the server's own rule, not a
// client-side validation error — the client sends it and reports what came
// back, which is exit 2.
func TestProjectsShowReportsAMalformedIdAsNotFound(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/me") {
			_, _ = w.Write([]byte(meWith("Acme Agency")))

			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("projects", "show", "not-a-uuid", "--env", "local")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stderr == "" {
		t.Error("a 404 should still explain itself on stderr")
	}
}

func TestProjectsPropagatesForbidden(t *testing.T) {
	h := newHarness(t, status(http.StatusForbidden, `{"message":"API access is not enabled for this account."}`))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("projects", "list", "--env", "local")

	if code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if !strings.Contains(stderr, "API access is not enabled") {
		t.Errorf("got:\n%s", stderr)
	}
}

// rowFor returns the one table line carrying an id, so an assertion about a
// cell cannot be satisfied by text from a different row.
func rowFor(t *testing.T, stdout, id string) string {
	t.Helper()

	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, id) {
			return line
		}
	}
	t.Fatalf("no row for %s in:\n%s", id, stdout)

	return ""
}

// See TestDealsListForwardsAnExplicitZeroLimit: all three list commands share
// this flag, so they must agree about what a typed zero means.
func TestProjectsListForwardsAnExplicitZeroLimit(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("projects", "list", "--limit", "0", "--env", "local")

	if !strings.Contains(query, "per_page=0") {
		t.Errorf("per_page=0 should reach the server, got query %q", query)
	}
}

func TestProjectsListOmitsAnUnsetLimit(t *testing.T) {
	var query string
	h := newHarness(t, projectsAPI(meWith("Acme Agency"), projectsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("projects", "list", "--env", "local")

	if strings.Contains(query, "per_page") {
		t.Errorf("an unset --limit should send no per_page, got query %q", query)
	}
}
