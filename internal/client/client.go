// Package client is a thin HTTP client for the Basa /api/v1 surface.
//
// It knows how to send a bearer token and how to turn an HTTP status into the
// CLI's error contract. It holds no domain logic — every response is handed
// back as decoded JSON for a command to render.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Basa-Futura/basa-cli/internal/fail"
)

// Client talks to one Basa environment.
type Client struct {
	baseURL string
	token   string
	env     string
	http    *http.Client
}

func New(env, baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		env:     env,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Me fetches the authenticated caller.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var envelope struct {
		Data Me `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/me", &envelope); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

// MeRaw fetches the caller as undecoded JSON, so --json can emit the API's own
// shape rather than a re-serialized approximation of it.
func (c *Client) MeRaw(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, "/api/v1/me", &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Me mirrors the fields of GET /api/v1/me that the CLI renders.
type Me struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Teams []Team `json:"teams"`
	Token struct {
		Name      string   `json:"name"`
		Abilities []string `json:"abilities"`
		ExpiresAt *string  `json:"expires_at"`
	} `json:"token"`
}

type Team struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Personal bool   `json:"personal"`
}

// --- deals -----------------------------------------------------------------

// Deal mirrors the fields of the deals endpoints that the CLI renders.
type Deal struct {
	ID string `json:"id"`
	// Stage is the coarse pipeline position: outreach, negotiation,
	// contracting, execution. It is what --stage filters on.
	Stage *struct {
		Slug  string `json:"slug"`
		Label string `json:"label"`
	} `json:"stage"`
	// Status is the finer lifecycle the web UI shows, derived server-side from
	// contracts, responses and negotiations. It is NOT the same thing as Stage,
	// and it is the one to read when the question is "what does my colleague
	// see in the browser": before the API exposed it, `deals list` and the web
	// gave different answers about the same deal.
	Status *struct {
		Slug  string `json:"slug"`
		Label string `json:"label"`
	} `json:"status"`
	Project *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Brand *struct {
		Name string `json:"name"`
	} `json:"brand"`
	Role *struct {
		Name string `json:"name"`
	} `json:"role"`
	Counterparty *struct {
		Name string `json:"name"`
	} `json:"counterparty"`
	AssignedTo *string `json:"assigned_to"`
	SentAt     *string `json:"sent_at"`
	UpdatedAt  *string `json:"updated_at"`
}

// DealPage is one page of deals plus the paginator metadata, so the CLI can
// tell the operator when there is more than they are seeing.
type DealPage struct {
	Deals []Deal
	Meta  PageMeta
}

type PageMeta struct {
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
	PerPage     int `json:"per_page"`
	Total       int `json:"total"`
}

// DealFilters are the query parameters the deals listing accepts.
type DealFilters struct {
	Stage   string
	Project string
	// Limit is a POINTER so that "the flag was not given" and "the flag was
	// given the value 0" stay distinguishable. They are not the same request:
	// omitting it means "let the server choose", while `--limit 0` is an
	// operator asking for a page size the server rejects with its 1-100
	// message. Collapsing the two returned a default page and reported it as
	// success -- the same defect ProjectFilters.Archived describes.
	Limit *int
	// Page is the paginator page to fetch. Zero means the server's first, and
	// only the page-walking helpers ever set it.
	Page int
}

func (f DealFilters) query() url.Values {
	q := url.Values{}
	if f.Stage != "" {
		q.Set("stage", f.Stage)
	}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Page > 0 {
		q.Set("page", strconv.Itoa(f.Page))
	}
	// Any value the operator actually typed is forwarded, 0 and negatives
	// included: the server's own 1-100 message is the right answer to an
	// invalid page size, and dropping it here returned a default page and
	// looked like success.
	if f.Limit != nil {
		q.Set("per_page", strconv.Itoa(*f.Limit))
	}
	return q
}

func (c *Client) Deals(ctx context.Context, teamID int64, filters DealFilters) (*DealPage, error) {
	var envelope struct {
		Data []Deal   `json:"data"`
		Meta PageMeta `json:"meta"`
	}
	if err := c.get(ctx, c.dealsPath(teamID, filters), &envelope); err != nil {
		return nil, err
	}
	return &DealPage{Deals: envelope.Data, Meta: envelope.Meta}, nil
}

// DealsRaw returns the listing undecoded, so --json emits the API's own shape
// including its paginator links rather than a re-serialized approximation.
func (c *Client) DealsRaw(ctx context.Context, teamID int64, filters DealFilters) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, c.dealsPath(teamID, filters), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// maxPageSize is the largest page the server will serve. Restated here for the
// same reason the --limit help text restates it: --all should cost a team with
// many deals as few requests as possible against the rate limit, and the
// server rejects anything larger with a 422 the client surfaces, so a wrong
// number here fails loudly rather than silently.
const maxPageSize = 100

// eachDealsPage fetches every page of a listing in order and hands each raw
// body to visit. The first page's paginator metadata decides how many follow.
func (c *Client) eachDealsPage(ctx context.Context, teamID int64, filters DealFilters, visit func(body []byte) error) error {
	pageSize := maxPageSize
	filters.Limit = &pageSize
	for page := 1; ; page++ {
		filters.Page = page

		var raw json.RawMessage
		if err := c.get(ctx, c.dealsPath(teamID, filters), &raw); err != nil {
			return err
		}
		var envelope struct {
			Meta PageMeta `json:"meta"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fail.Wrap(fail.CodeUsage, "The server sent a response this version of basa could not read.", err)
		}
		if err := visit(raw); err != nil {
			return err
		}
		if page >= envelope.Meta.LastPage {
			return nil
		}
	}
}

// DealsAll returns every deal the filters match, across all pages. The
// paginator metadata is rewritten to describe the combined result, because
// that is what the caller is now holding.
func (c *Client) DealsAll(ctx context.Context, teamID int64, filters DealFilters) (*DealPage, error) {
	all := &DealPage{Deals: []Deal{}}
	err := c.eachDealsPage(ctx, teamID, filters, func(body []byte) error {
		var envelope struct {
			Data []Deal `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return fail.Wrap(fail.CodeUsage, "The server sent a response this version of basa could not read.", err)
		}
		all.Deals = append(all.Deals, envelope.Data...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	all.Meta = PageMeta{CurrentPage: 1, LastPage: 1, PerPage: len(all.Deals), Total: len(all.Deals)}
	return all, nil
}

// DealsAllRaw is DealsAll for --json: every page's items spliced into one
// envelope, each item byte-for-byte as the server sent it, so a field this
// version knows nothing about still reaches the caller.
func (c *Client) DealsAllRaw(ctx context.Context, teamID int64, filters DealFilters) (json.RawMessage, error) {
	items := []json.RawMessage{}
	err := c.eachDealsPage(ctx, teamID, filters, func(body []byte) error {
		var envelope struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return fail.Wrap(fail.CodeUsage, "The server sent a response this version of basa could not read.", err)
		}
		items = append(items, envelope.Data...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(struct {
		Data []json.RawMessage `json:"data"`
		Meta PageMeta          `json:"meta"`
	}{items, PageMeta{CurrentPage: 1, LastPage: 1, PerPage: len(items), Total: len(items)}})
	if err != nil {
		return nil, fail.Wrap(fail.CodeUsage, "Could not assemble the combined listing.", err)
	}
	return out, nil
}

func (c *Client) Deal(ctx context.Context, teamID int64, id string) (*Deal, error) {
	var envelope struct {
		Data Deal `json:"data"`
	}
	if err := c.get(ctx, c.dealPath(teamID, id), &envelope); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (c *Client) DealRaw(ctx context.Context, teamID int64, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, c.dealPath(teamID, id), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) dealsPath(teamID int64, filters DealFilters) string {
	path := fmt.Sprintf("/api/v1/teams/%d/deals", teamID)
	if q := filters.query(); len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

func (c *Client) dealPath(teamID int64, id string) string {
	// The id is a Sqid from our own API, but escape it anyway rather than
	// trusting the shape of a value that arrived on the command line.
	return fmt.Sprintf("/api/v1/teams/%d/deals/%s", teamID, url.PathEscape(id))
}

// --- contracts -------------------------------------------------------------

// Contract mirrors the fields of the contracts endpoints that the CLI renders.
type Contract struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Status *struct {
		Slug  string `json:"slug"`
		Label string `json:"label"`
	} `json:"status"`
	Sequence int `json:"sequence"`
	// Null for a standalone contract, which has no deal at all.
	Deal *struct {
		ID string `json:"id"`
	} `json:"deal"`
	SenderTeam *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"sender_team"`
	Recipient  *string `json:"recipient"`
	SignedAt   *string `json:"signed_at"`
	DeclinedAt *string `json:"declined_at"`
	UpdatedAt  *string `json:"updated_at"`
}

type ContractPage struct {
	Contracts []Contract
	Meta      PageMeta
}

// ContractFilters are the query parameters the contracts listing accepts.
type ContractFilters struct {
	Status string
	// Pointer: see DealFilters.Limit.
	Limit *int
}

func (f ContractFilters) query() url.Values {
	q := url.Values{}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	// Pointer for the same reason as DealFilters: an out-of-range value, 0
	// included, is the operator asking for something invalid, and the server
	// owns that message.
	if f.Limit != nil {
		q.Set("per_page", strconv.Itoa(*f.Limit))
	}
	return q
}

func (c *Client) Contracts(ctx context.Context, teamID int64, filters ContractFilters) (*ContractPage, error) {
	var envelope struct {
		Data []Contract `json:"data"`
		Meta PageMeta   `json:"meta"`
	}
	if err := c.get(ctx, c.contractsPath(teamID, filters), &envelope); err != nil {
		return nil, err
	}
	return &ContractPage{Contracts: envelope.Data, Meta: envelope.Meta}, nil
}

func (c *Client) ContractsRaw(ctx context.Context, teamID int64, filters ContractFilters) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, c.contractsPath(teamID, filters), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) Contract(ctx context.Context, teamID int64, id string) (*Contract, error) {
	var envelope struct {
		Data Contract `json:"data"`
	}
	if err := c.get(ctx, c.contractPath(teamID, id), &envelope); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (c *Client) ContractRaw(ctx context.Context, teamID int64, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, c.contractPath(teamID, id), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) contractsPath(teamID int64, filters ContractFilters) string {
	path := fmt.Sprintf("/api/v1/teams/%d/contracts", teamID)
	if q := filters.query(); len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

func (c *Client) contractPath(teamID int64, id string) string {
	return fmt.Sprintf("/api/v1/teams/%d/contracts/%s", teamID, url.PathEscape(id))
}

// --- projects --------------------------------------------------------------

// Project mirrors the fields of the projects endpoints that the CLI renders.
type Project struct {
	// A UUID, not a Sqid: `projects.id` is a uuid column, so unlike a deal or
	// a contract id this is 36 characters and already opaque. It is also the
	// value `deals list --project` takes.
	ID   string `json:"id"`
	Name string `json:"name"`
	// The buyer-set name talent sees on the outreach surface, distinct from the
	// internal Name and frequently null.
	ExternalName *string `json:"external_name"`
	// May name a client owned by another team — a declared exception the web
	// makes too, so the API does and this renders it.
	Brand *struct {
		Name string `json:"name"`
	} `json:"brand"`
	Archived    bool    `json:"archived"`
	Type        *string `json:"type"`
	NDARequired bool    `json:"nda_required"`
	CreatedAt   *string `json:"created_at"`
	UpdatedAt   *string `json:"updated_at"`
}

type ProjectPage struct {
	Projects []Project
	Meta     PageMeta
}

// ProjectFilters are the query parameters the projects listing accepts.
type ProjectFilters struct {
	// Archived is forwarded verbatim rather than modelled as a bool, because
	// the server's parameter is a tri-state: absent or "false" is active only,
	// "true" is archived only, "all" is both. A bool cannot say three things,
	// and the server already owns the message for anything else ("archived
	// must be one of: true, false, all.").
	//
	// It is a POINTER so that "the flag was not given" and "the flag was given
	// an empty value" stay distinguishable. They are not the same request: the
	// first is the server's active-only default, while `--archived=` — which a
	// shell writes whenever an interpolated variable is empty — is an operator
	// asking for something invalid, and the server answers it with a 422 naming
	// the valid values. Collapsing the two returned an active-only page and
	// looked like success. Limit carries the same shape, for the same reason.
	Archived *string
	Search   string
	// Pointer: see DealFilters.Limit.
	Limit *int
}

func (f ProjectFilters) query() url.Values {
	q := url.Values{}
	if f.Archived != nil {
		q.Set("archived", *f.Archived)
	}
	// Search stays a plain string: unlike archived, an empty value and an
	// absent one are the same request. `search=` passes the server's
	// `max:255` rule and filters nothing, which is exactly what omitting it
	// does, so there is no message being swallowed here and nothing to carry.
	if f.Search != "" {
		q.Set("search", f.Search)
	}
	// Pointer for the same reason as DealFilters: an out-of-range value, 0
	// included, is the operator asking for something invalid, and the server
	// owns that message.
	if f.Limit != nil {
		q.Set("per_page", strconv.Itoa(*f.Limit))
	}
	return q
}

func (c *Client) Projects(ctx context.Context, teamID int64, filters ProjectFilters) (*ProjectPage, error) {
	var envelope struct {
		Data []Project `json:"data"`
		Meta PageMeta  `json:"meta"`
	}
	if err := c.get(ctx, c.projectsPath(teamID, filters), &envelope); err != nil {
		return nil, err
	}
	return &ProjectPage{Projects: envelope.Data, Meta: envelope.Meta}, nil
}

func (c *Client) ProjectsRaw(ctx context.Context, teamID int64, filters ProjectFilters) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, c.projectsPath(teamID, filters), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) Project(ctx context.Context, teamID int64, id string) (*Project, error) {
	var envelope struct {
		Data Project `json:"data"`
	}
	if err := c.get(ctx, c.projectPath(teamID, id), &envelope); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (c *Client) ProjectRaw(ctx context.Context, teamID int64, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.get(ctx, c.projectPath(teamID, id), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) projectsPath(teamID int64, filters ProjectFilters) string {
	path := fmt.Sprintf("/api/v1/teams/%d/projects", teamID)
	if q := filters.query(); len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

// projectPath escapes the id rather than validating its shape. The server
// accepts either the long UUID its own responses carry or the short form the
// web's URLs use, and answers anything that cannot name a project with a 404 —
// so a client-side well-formedness check would only duplicate a server rule
// and risk disagreeing with it.
func (c *Client) projectPath(teamID int64, id string) string {
	return fmt.Sprintf("/api/v1/teams/%d/projects/%s", teamID, url.PathEscape(id))
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fail.Usagef("Could not build the request: %v", err)
	}

	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fail.Unreachable(c.env, c.baseURL)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		// A reply that is complete JSON but shorter than declared is a dropped
		// connection, not an answer. Decoding what did arrive would report the
		// first part of a result as the whole of one.
		return fail.Wrap(fail.CodeUsage, "The connection was cut off before the server finished its reply — try again.", err)
	}

	if err := c.statusError(resp.StatusCode, resp.Header, body); err != nil {
		return err
	}

	if into == nil {
		return nil
	}

	if err := json.Unmarshal(body, into); err != nil {
		return fail.Wrap(fail.CodeUsage, "The server sent a response this version of basa could not read.", err)
	}

	return nil
}

// statusError maps HTTP status onto the CLI's exit-code contract. Each branch
// tells the operator what to do, not what went wrong internally.
func (c *Client) statusError(status int, header http.Header, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}

	switch status {
	case http.StatusUnauthorized:
		return fail.TokenRejected(c.env)

	case http.StatusForbidden:
		// 403 is either "API access is off for this account" or "this token
		// lacks the ability". The server's own message distinguishes them, so
		// prefer it over anything invented here.
		msg, hint := apiMessage(body)
		if msg == "" {
			msg = "You do not have access to that."
		}
		if hint == "" {
			hint = "Ask a Basa administrator to enable API access for you."
		}
		return fail.Forbidden(msg, hint)

	case http.StatusNotFound:
		return fail.NotFound("That does not exist, or you cannot see it.")

	case http.StatusTooManyRequests:
		return fail.RateLimited(retryAfter(header, time.Now()))

	case http.StatusUnprocessableEntity:
		msg, _ := apiMessage(body)
		if msg == "" {
			msg = "The request was rejected as invalid."
		}
		return fail.Usage(msg)
	}

	if status >= 500 {
		return fail.ServerError(c.env, status)
	}

	return fail.Usagef("Unexpected response from the server (%d).", status)
}

// retryAfter reads RFC 9110's Retry-After, which is either a count of seconds
// or an HTTP date. Laravel's throttle sends seconds; the date form is parsed
// too, so a proxy or CDN in front of the API cannot make the CLI go quiet about
// how long the wait is.
//
// now is a parameter rather than a call to time.Now so the date branch is
// testable without sleeping. A zero return means "the server did not say" —
// including a header that has already elapsed, which is no longer a wait.
func retryAfter(header http.Header, now time.Time) time.Duration {
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return 0
	}

	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}

	if when, err := http.ParseTime(raw); err == nil {
		if wait := when.Sub(now); wait > 0 {
			return wait
		}
	}

	return 0
}

// apiMessage pulls Laravel's conventional `message` field, plus our `hint`.
func apiMessage(body []byte) (msg, hint string) {
	var payload struct {
		Message string `json:"message"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	return payload.Message, payload.Hint
}
