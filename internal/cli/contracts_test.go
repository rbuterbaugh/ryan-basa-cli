package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Basa-Futura/basa-cli/internal/config"
)

const contractsBody = `{"data":[
  {"id":"Qp7Wn","name":"Spring Campaign agreement",
   "status":{"slug":"ready_for_signature","label":"Ready for Signature"},
   "sequence":1,"deal":{"id":"K3mQz"},
   "sender_team":{"id":7,"name":"Acme Agency"},
   "recipient":"Sam Rivera","signed_at":null,"declined_at":null,
   "updated_at":"2026-08-18T09:30:00+00:00"},
  {"id":"Rt2Bk","name":"Quick agreement",
   "status":{"slug":"draft","label":"Draft"},
   "sequence":1,"deal":null,
   "sender_team":{"id":7,"name":"Acme Agency"},
   "recipient":"Jordan Lee","signed_at":null,"declined_at":null,
   "updated_at":"2026-08-17T09:30:00+00:00"}
],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":2}}`

// contractsAPI routes /me and the contracts endpoints, recording the last query
// so tests can assert filters reached the server.
func contractsAPI(me, contracts string, lastQuery *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/me"):
			_, _ = w.Write([]byte(me))
		case strings.Contains(r.URL.Path, "/contracts"):
			if lastQuery != nil {
				*lastQuery = r.URL.RawQuery
			}
			_, _ = w.Write([]byte(contracts))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no route"}`))
		}
	}
}

func TestContractsListRendersATable(t *testing.T) {
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("contracts", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"ID", "STATUS", "RECIPIENT", "Qp7Wn", "Ready for Signature", "Sam Rivera"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table is missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
}

// A contract with no deal is a real, meaningful state, not missing data. It
// must read as such rather than as a blank cell.
func TestAStandaloneContractIsLabelledNotBlank(t *testing.T) {
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("contracts", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "standalone") {
		t.Errorf("a null deal should read as standalone, got:\n%s", stdout)
	}
	// And the deal-backed row still shows its deal id.
	if !strings.Contains(stdout, "K3mQz") {
		t.Errorf("deal-backed row lost its deal id:\n%s", stdout)
	}
}

func TestContractsListJSONEmitsTheAPIShape(t *testing.T) {
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, _, code := h.run("contracts", "list", "--env", "local", "--json")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON:\n%s", stdout)
	}

	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Deal *struct {
				ID string `json:"id"`
			} `json:"deal"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 contracts, got %d", len(payload.Data))
	}
	// The null deal must survive as null, not be invented into an empty object.
	if payload.Data[1].Deal != nil {
		t.Errorf("standalone contract should have a null deal, got %+v", payload.Data[1].Deal)
	}
	if payload.Meta.Total != 2 {
		t.Errorf("meta.total did not survive: %d", payload.Meta.Total)
	}
}

func TestContractsListPassesTheStatusFilter(t *testing.T) {
	var query string
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("contracts", "list", "--env", "local", "--status", "ready_for_signature", "--limit", "3")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(query, "status=ready_for_signature") {
		t.Errorf("status filter did not reach the server, query was %q", query)
	}
	if !strings.Contains(query, "per_page=3") {
		t.Errorf("--limit did not become per_page, query was %q", query)
	}
}

// Same defect the deals commands had: a stray positional was ignored, so
// `basa contracts list <id>` returned the whole list and exited 0.
func TestContractsListRejectsAStrayArgument(t *testing.T) {
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("contracts", "list", "EfhxL", "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("nothing should reach stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "contracts show") {
		t.Errorf("the error should point at show, got:\n%s", stderr)
	}
}

func TestContractsShowRejectsASecondArgument(t *testing.T) {
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, code := h.run("contracts", "show", "EfhxL", "VqXmZ", "--env", "local")

	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// An out-of-range limit must reach the server so its own 1-100 message is what
// the operator sees, rather than a silently default-sized page.
func TestContractsListForwardsAnOutOfRangeLimit(t *testing.T) {
	var query string
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("contracts", "list", "--limit", "-1", "--env", "local")

	if !strings.Contains(query, "per_page=-1") {
		t.Errorf("per_page=-1 should reach the server, got query %q", query)
	}
}

func TestContractsListSaysSoWhenEmpty(t *testing.T) {
	empty := `{"data":[],"meta":{"current_page":1,"last_page":1,"per_page":25,"total":0}}`
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), empty, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("contracts", "list", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0 — an empty result is not an error", code)
	}
	if !strings.Contains(strings.ToLower(stderr), "no contracts") {
		t.Errorf("should say nothing matched, got:\n%s", stderr)
	}
}

func TestContractsListWarnsWhenTruncated(t *testing.T) {
	truncated := strings.Replace(contractsBody, `"total":2`, `"total":31`, 1)
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), truncated, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, _ := h.run("contracts", "list", "--env", "local")

	if !strings.Contains(stderr, "of 31") {
		t.Errorf("should warn that more exist, got:\n%s", stderr)
	}
}

func TestContractsShowRendersARecord(t *testing.T) {
	single := `{"data":{"id":"Qp7Wn","name":"Spring Campaign agreement",
	  "status":{"slug":"signed","label":"Signed"},"sequence":2,
	  "deal":{"id":"K3mQz"},"sender_team":{"id":7,"name":"Acme Agency"},
	  "recipient":"Sam Rivera","signed_at":"2026-08-18T09:30:00+00:00",
	  "declined_at":null,"updated_at":"2026-08-18T09:30:00+00:00"}}`

	h := newHarness(t, contractsAPI(meWith("Acme Agency"), single, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	stdout, stderr, code := h.run("contracts", "show", "Qp7Wn", "--env", "local")

	if code != 0 {
		t.Fatalf("exit %d, want 0. stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"Contract", "Qp7Wn", "Signed", "Sam Rivera", "Version", "2026-08-18"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("record is missing %q\n--- stdout ---\n%s", want, stdout)
		}
	}
	// An absent declined_at must read as a dash, not "<nil>" or an empty gap.
	if strings.Contains(stdout, "<nil>") {
		t.Errorf("a null field leaked as <nil>:\n%s", stdout)
	}
}

func TestContractsShowRequiresAnId(t *testing.T) {
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, nil))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("contracts", "show", "--env", "local")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "Which contract") {
		t.Errorf("got:\n%s", stderr)
	}
}

func TestContractsPropagatesForbidden(t *testing.T) {
	h := newHarness(t, status(http.StatusForbidden, `{"message":"API access is not enabled for this account."}`))
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("contracts", "list", "--env", "local")

	if code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if !strings.Contains(stderr, "API access is not enabled") {
		t.Errorf("got:\n%s", stderr)
	}
}

// The server validates the status against its own table, so an unknown one
// arrives as a 422 whose message names the valid values. That message is more
// useful than anything the client could invent, so it must be surfaced.
func TestContractsSurfacesTheServersValidationMessage(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/v1/me") {
			_, _ = w.Write([]byte(meWith("Acme Agency")))

			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Unknown status. Valid statuses are: draft, ready_for_signature, signed, declined, voided."}`))
	})
	t.Setenv(config.EnvVarToken, "42|token")

	_, stderr, code := h.run("contracts", "list", "--env", "local", "--status", "bogus")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "Valid statuses are") {
		t.Errorf("should surface the server's own message, got:\n%s", stderr)
	}
}

// See TestDealsListForwardsAnExplicitZeroLimit: all three list commands share
// this flag, so they must agree about what a typed zero means.
func TestContractsListForwardsAnExplicitZeroLimit(t *testing.T) {
	var query string
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("contracts", "list", "--limit", "0", "--env", "local")

	if !strings.Contains(query, "per_page=0") {
		t.Errorf("per_page=0 should reach the server, got query %q", query)
	}
}

func TestContractsListOmitsAnUnsetLimit(t *testing.T) {
	var query string
	h := newHarness(t, contractsAPI(meWith("Acme Agency"), contractsBody, &query))
	t.Setenv(config.EnvVarToken, "42|token")

	_, _, _ = h.run("contracts", "list", "--env", "local")

	if strings.Contains(query, "per_page") {
		t.Errorf("an unset --limit should send no per_page, got query %q", query)
	}
}
