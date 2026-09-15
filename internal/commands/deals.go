package commands

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Basa-Futura/basa-cli/internal/client"
	"github.com/Basa-Futura/basa-cli/internal/fail"
	"github.com/Basa-Futura/basa-cli/internal/output"
)

// NewDealsCmd builds the `basa deals` group.
func NewDealsCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deals",
		Aliases: []string{"deal"},
		Short:   "List and show deals",
		Long: `List and show deals.

A deal is one creator on one campaign. "Outstanding" usually means filtering by
stage — outreach, negotiation, contracting, or execution.`,
		Example: `  basa deals list --env staging
  basa deals list --env staging --stage contracting
  basa deals show K3mQz --env staging`,
	}

	cmd.AddCommand(newDealsListCmd(deps), newDealsShowCmd(deps))

	return cmd
}

func newDealsListCmd(deps *Deps) *cobra.Command {
	var (
		stage   string
		project string
		limit   int
		all     bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the team's deals",
		Args:  rejectStrayArgs("deals list", "deals show <id>"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			filters := client.DealFilters{Stage: stage, Project: project}
			// Whether --limit was typed at all, not whether its value is
			// non-zero. `--limit 0` must reach the server so its 1-100 message
			// answers it, instead of being folded into the default page size
			// and reported as a successful listing.
			if cmd.Flags().Changed("limit") {
				filters.Limit = &limit
			}

			return runDealsList(cmd.Context(), deps, filters, all)
		},
	}

	cmd.Flags().StringVar(&stage, "stage", "", "Only this stage (outreach, negotiation, contracting, execution)")
	cmd.Flags().StringVar(&project, "project", "", "Only this project (a project id)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "How many to show (1-100, default 25)")
	cmd.Flags().BoolVar(&all, "all", false, "Every page, not just the first (cannot be combined with --limit)")

	return cmd
}

func runDealsList(ctx context.Context, deps *Deps, filters client.DealFilters, all bool) error {
	if all && filters.Limit != nil {
		return fail.UsageHint(
			"--all fetches every page at the server's largest page size, so --limit has nothing left to set.",
			"Pass one or the other.",
		)
	}

	c, env, err := clientFor(deps)
	if err != nil {
		return err
	}

	team, err := resolveTeam(ctx, deps, c)
	if err != nil {
		return err
	}

	if deps.Out.JSON {
		fetch := c.DealsRaw
		if all {
			fetch = c.DealsAllRaw
		}
		raw, err := fetch(ctx, team.ID, filters)
		if err != nil {
			return err
		}
		return deps.Out.Data(raw, nil)
	}

	fetch := c.Deals
	if all {
		fetch = c.DealsAll
	}
	page, err := fetch(ctx, team.ID, filters)
	if err != nil {
		return err
	}

	// The operator should always know which system and which team they are
	// looking at, and that belongs on stderr so it cannot corrupt a pipeline.
	// When every deal on the page belongs to one project, that is said here
	// too — once, instead of on every row of the table below.
	heading := env + " · " + team.Name
	project, brand, oneProject := uniformProject(page.Deals)
	if oneProject {
		heading += " · " + project
		if brand != "" {
			heading += " (" + brand + ")"
		}
	}
	deps.Out.Notice("%s", heading)

	if len(page.Deals) == 0 {
		deps.Out.Notice("No deals matched.")

		return nil
	}

	groups := groupByStatus(page.Deals)
	if len(groups) > 1 {
		deps.Out.Notice("%s", tally(groups, len(page.Deals)))
	}

	headers := []string{"ID", "STATUS", "STAGE"}
	if !oneProject {
		headers = append(headers, "PROJECT", "BRAND")
	}
	headers = append(headers, "COUNTERPARTY", "ROLE", "ASSIGNED", "UPDATED")

	rows := make([][]string, 0, len(page.Deals))
	for _, g := range groups {
		for _, deal := range g.deals {
			row := []string{deal.ID, dealStatus(deal), dealStage(deal)}
			if !oneProject {
				row = append(row, dealProject(deal), dealBrand(deal))
			}
			rows = append(rows, append(row,
				dealCounterparty(deal),
				valueOr(deal.Role != nil, func() string { return deal.Role.Name }),
				derefOr(deal.AssignedTo, "—"),
				shortDate(deal.UpdatedAt),
			))
		}
	}

	if err := deps.Out.Data(nil, func(io.Writer) error {
		return deps.Out.Table(output.Table{Headers: headers, Rows: rows})
	}); err != nil {
		return err
	}

	// Say so when there is more than they are seeing. Silently truncating a
	// list reads as "this is everything", which for a status report is worse
	// than showing nothing. --all has already fetched everything.
	if !all && page.Meta.Total > len(page.Deals) {
		deps.Out.Notice("Showing %d of %d. Use --limit to see more, or --all for everything.", len(page.Deals), page.Meta.Total)
	}

	return nil
}

// uniformProject reports the one project every deal belongs to, with its brand,
// when there is exactly one — matched on id, displayed by name. The brand is
// blank if the deals disagree about it, which should not happen for one project
// but costs nothing to tolerate.
func uniformProject(deals []client.Deal) (project, brand string, ok bool) {
	if len(deals) == 0 || deals[0].Project == nil {
		return "", "", false
	}
	first := deals[0]
	project, brand = first.Project.Name, dealBrand(first)
	for _, d := range deals[1:] {
		if d.Project == nil || d.Project.ID != first.Project.ID {
			return "", "", false
		}
		if dealBrand(d) != brand {
			brand = ""
		}
	}
	if brand == "—" {
		brand = ""
	}
	return project, brand, true
}

// statusGroup is the deals sharing one status label, and when any of them was
// last touched.
type statusGroup struct {
	label  string
	latest time.Time
	deals  []client.Deal
}

// groupByStatus orders deals so those sharing a status sit together, the group
// touched most recently first, and the server's order kept within each group.
//
// This is presentation, not judgement. The client does not know that "On Hold"
// matters less than "Ready to Send" — deciding what a status means is the
// server's job and stays there. It knows only that thirty rows with one label
// read better as a block than interleaved, and that a block nobody has touched
// since May belongs below one touched this morning.
func groupByStatus(deals []client.Deal) []statusGroup {
	var groups []statusGroup
	index := map[string]int{}
	for _, d := range deals {
		label := dealStatus(d)
		i, seen := index[label]
		if !seen {
			i = len(groups)
			index[label] = i
			groups = append(groups, statusGroup{label: label})
		}
		groups[i].deals = append(groups[i].deals, d)
		if t := updatedTime(d); t.After(groups[i].latest) {
			groups[i].latest = t
		}
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a].latest.After(groups[b].latest) })
	return groups
}

// updatedTime reads a deal's updated_at for ordering. An unreadable or absent
// timestamp sorts as the zero time, which puts its group last — the honest
// place for "we do not know when this moved".
//
// RFC3339 is the only layout needed, including for fractional seconds: when
// parsing, Go accepts a fractional second immediately after the seconds field
// even though the layout does not mention one. A test pins that, because it
// reads like a gap and is not one.
func updatedTime(d client.Deal) time.Time {
	if d.UpdatedAt == nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, *d.UpdatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// tally is the shape of the list in one line — "47 deals · 30 On Hold · 8
// Completed …" — in the same order as the table below it, so the two agree. It
// goes on stderr with the rest of the context: a table cannot say "thirty of
// these are parked", and that is the headline.
func tally(groups []statusGroup, total int) string {
	parts := []string{fmt.Sprintf("%d %s", total, nounCount(total, "deal"))}
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%d %s", len(g.deals), g.label))
	}
	return strings.Join(parts, " · ")
}

func nounCount(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

func newDealsShowCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one deal",
		// At most one, rather than exactly one, so a bare `deals show` still
		// gets the question below instead of Cobra's wording.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fail.UsageHint("Which deal?", "Pass its id: basa deals show <id> --env <name>")
			}

			return runDealsShow(cmd.Context(), deps, args[0])
		},
	}
}

func runDealsShow(ctx context.Context, deps *Deps, id string) error {
	c, env, err := clientFor(deps)
	if err != nil {
		return err
	}

	team, err := resolveTeam(ctx, deps, c)
	if err != nil {
		return err
	}

	if deps.Out.JSON {
		raw, err := c.DealRaw(ctx, team.ID, id)
		if err != nil {
			return err
		}
		return deps.Out.Data(raw, nil)
	}

	deal, err := c.Deal(ctx, team.ID, id)
	if err != nil {
		return err
	}

	return deps.Out.Data(nil, func(io.Writer) error {
		return deps.Out.Record(output.Record{Fields: []output.Field{
			{Key: "Environment", Value: env},
			{Key: "Team", Value: team.Name},
			{Key: "Deal", Value: deal.ID},
			{Key: "Stage", Value: dealStage(*deal)},
			{Key: "Status", Value: dealStatus(*deal)},
			{Key: "Project", Value: dealProject(*deal)},
			{Key: "Brand", Value: dealBrand(*deal)},
			{Key: "Role", Value: valueOr(deal.Role != nil, func() string { return deal.Role.Name })},
			{Key: "Counterparty", Value: dealCounterparty(*deal)},
			{Key: "Assigned to", Value: derefOr(deal.AssignedTo, "nobody")},
			{Key: "Sent", Value: shortDate(deal.SentAt)},
			{Key: "Updated", Value: shortDate(deal.UpdatedAt)},
		}})
	})
}

// --- team resolution -------------------------------------------------------

// resolveTeam turns --team into a team, and refuses to guess when it is absent
// and the caller belongs to more than one.
//
// A single team IS resolved implicitly here, unlike --env. The difference is
// consequence: picking the wrong environment can mean reading or eventually
// writing the wrong system, whereas picking the wrong team just shows the wrong
// list, and the team name is printed on every result so a mistake is visible.
// rejectStrayArgs refuses positional arguments on a command that takes none.
// cobra.NoArgs would also reject them, but its message ("accepts 0 arg(s),
// received 1") is not the voice the rest of this CLI speaks, and the likely
// cause is reaching for the sibling command — so the error names it.
//
// Without this, `basa deals list <id>` listed everything and exited 0, which
// reads as "that id matched all of these".
func rejectStrayArgs(command, instead string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}

		return fail.UsageHintf(
			"Did you mean: basa "+instead,
			"basa %s takes no arguments, but got %q.", command, args[0],
		)
	}
}

func resolveTeam(ctx context.Context, deps *Deps, c *client.Client) (client.Team, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return client.Team{}, err
	}

	if len(me.Teams) == 0 {
		return client.Team{}, fail.UsageHint(
			"Your account is not a member of any team.",
			"Ask a Basa administrator to add you to one.",
		)
	}

	if deps.TeamFlag == "" {
		if len(me.Teams) == 1 {
			return me.Teams[0], nil
		}

		return client.Team{}, fail.UsageHint(
			"You belong to more than one team, so basa cannot tell which you mean.",
			"Pass --team with one of: "+teamNames(me.Teams),
		)
	}

	matches := matchTeams(me.Teams, deps.TeamFlag)

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return client.Team{}, fail.UsageHint(
			fmt.Sprintf("No team of yours matches %q.", deps.TeamFlag),
			"Your teams: "+teamNames(me.Teams),
		)
	default:
		return client.Team{}, fail.UsageHint(
			fmt.Sprintf("%q matches more than one of your teams.", deps.TeamFlag),
			"Be more specific: "+teamNames(matches),
		)
	}
}

// matchTeams accepts an exact id, then an exact name, then a unique substring —
// so an operator can type a memorable fragment instead of an integer, but a
// fragment that could mean two teams is an error rather than a coin flip.
//
// The name pass returns every exact match, not the first. Two teams sharing a
// name is a real shape, and resolveTeam turns a plural here into "be more
// specific", with ids, rather than this function quietly choosing one.
func matchTeams(teams []client.Team, want string) []client.Team {
	if id, err := strconv.ParseInt(want, 10, 64); err == nil {
		for _, t := range teams {
			if t.ID == id {
				return []client.Team{t}
			}
		}
	}

	needle := strings.ToLower(strings.TrimSpace(want))

	var exact []client.Team
	for _, t := range teams {
		if strings.ToLower(t.Name) == needle {
			exact = append(exact, t)
		}
	}
	if len(exact) > 0 {
		return exact
	}

	var partial []client.Team
	for _, t := range teams {
		if strings.Contains(strings.ToLower(t.Name), needle) {
			partial = append(partial, t)
		}
	}

	return partial
}

func teamNames(teams []client.Team) string {
	names := make([]string, 0, len(teams))
	for _, t := range teams {
		names = append(names, fmt.Sprintf("%s (%d)", t.Name, t.ID))
	}
	return strings.Join(names, ", ")
}

// --- formatting ------------------------------------------------------------

func dealStage(d client.Deal) string {
	if d.Stage == nil {
		return "—"
	}
	return d.Stage.Label
}

// Nil for an older server that does not send `status` yet, which renders the
// same em dash as any other absent field rather than an empty column.
func dealStatus(d client.Deal) string {
	if d.Status == nil {
		return "—"
	}
	return d.Status.Label
}

func dealProject(d client.Deal) string {
	if d.Project == nil {
		return "—"
	}
	return d.Project.Name
}

func dealBrand(d client.Deal) string {
	if d.Brand == nil {
		return "—"
	}
	return d.Brand.Name
}

func dealCounterparty(d client.Deal) string {
	if d.Counterparty == nil {
		return "—"
	}
	return d.Counterparty.Name
}

// shortDate trims an ISO-8601 timestamp to the date. A COO scanning a list
// wants the day, not the second.
func shortDate(iso *string) string {
	if iso == nil || *iso == "" {
		return "—"
	}
	if len(*iso) >= 10 {
		return (*iso)[:10]
	}
	return *iso
}

func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func valueOr(ok bool, get func() string) string {
	if !ok {
		return "—"
	}
	return get()
}
