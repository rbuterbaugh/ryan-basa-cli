package commands

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/Basa-Futura/basa-cli/internal/client"
	"github.com/Basa-Futura/basa-cli/internal/fail"
	"github.com/Basa-Futura/basa-cli/internal/output"
)

// NewProjectsCmd builds the `basa projects` group.
func NewProjectsCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "List and show projects",
		Long: `List and show projects.

A project is one campaign: a brand, a set of roles, and the deals run under it.
"What is active" is the default — archived projects are hidden until asked for,
the same way the web hides them.

This is also where an id for basa deals list --project comes from.`,
		Example: `  basa projects list --env staging
  basa projects list --env staging --archived all
  basa projects list --env staging --search northwind
  basa projects show 9f2c1111-2222-3333-4444-555566667777 --env staging`,
	}

	cmd.AddCommand(newProjectsListCmd(deps), newProjectsShowCmd(deps))

	return cmd
}

func newProjectsListCmd(deps *Deps) *cobra.Command {
	var (
		archived string
		search   string
		limit    int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the team's projects",
		Args:  rejectStrayArgs("projects list", "projects show <id>"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			filters := client.ProjectFilters{Search: search}
			// Same reasoning as --archived below: a typed --limit 0 belongs to
			// the server's validation, not to this client's default.
			if cmd.Flags().Changed("limit") {
				filters.Limit = &limit
			}
			// Whether the operator typed --archived at all, not whether what
			// they typed was non-empty. An empty value has to reach the server
			// so its own message answers it, rather than being folded into the
			// active-only default and reported as a successful listing.
			if cmd.Flags().Changed("archived") {
				filters.Archived = &archived
			}

			return runProjectsList(cmd.Context(), deps, filters)
		},
	}

	// A string rather than a bool, because the server's parameter has three
	// states and a bool can only carry two. Anything else is forwarded and the
	// server names the valid values, as it does for --limit.
	cmd.Flags().StringVar(&archived, "archived", "", `Which to include: "false" for active (the default), "true" for archived only, "all" for both`)
	cmd.Flags().StringVar(&search, "search", "", "Only those whose name, or whose brand's name, contains this")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "How many to show (1-100, default 25)")

	return cmd
}

func runProjectsList(ctx context.Context, deps *Deps, filters client.ProjectFilters) error {
	c, env, err := clientFor(deps)
	if err != nil {
		return err
	}

	team, err := resolveTeam(ctx, deps, c)
	if err != nil {
		return err
	}

	if deps.Out.JSON {
		raw, err := c.ProjectsRaw(ctx, team.ID, filters)
		if err != nil {
			return err
		}
		return deps.Out.Data(raw, nil)
	}

	page, err := c.Projects(ctx, team.ID, filters)
	if err != nil {
		return err
	}

	// Which system and which team, always, on stderr so it cannot corrupt a
	// pipeline. Anything every row on the page agrees about is said here once
	// instead of in a column repeating it — and the column comes back the
	// moment a second value appears.
	heading := env + " · " + team.Name
	brand, oneBrand := uniformBrand(page.Projects)
	if oneBrand {
		heading += " · " + brand
	}
	mixedArchived, allArchived := archivedSpread(page.Projects)
	if !mixedArchived && allArchived {
		heading += " · archived"
	}
	deps.Out.Notice("%s", heading)

	if len(page.Projects) == 0 {
		deps.Out.Notice("No projects matched.")

		return nil
	}

	headers := []string{"ID", "NAME"}
	if !oneBrand {
		headers = append(headers, "BRAND")
	}
	headers = append(headers, "TYPE", "NDA")
	// Only when the page actually holds both. Active is the default listing, so
	// a column reading "no" on every row would be noise; a page that is
	// uniformly archived says so in the heading above.
	if mixedArchived {
		headers = append(headers, "ARCHIVED")
	}
	headers = append(headers, "UPDATED")

	rows := make([][]string, 0, len(page.Projects))
	for _, project := range page.Projects {
		row := []string{project.ID, project.Name}
		if !oneBrand {
			row = append(row, projectBrand(project))
		}
		row = append(row, derefOr(project.Type, "—"), yesNo(project.NDARequired))
		if mixedArchived {
			row = append(row, yesNo(project.Archived))
		}
		rows = append(rows, append(row, shortDate(project.UpdatedAt)))
	}

	if err := deps.Out.Data(nil, func(io.Writer) error {
		return deps.Out.Table(output.Table{Headers: headers, Rows: rows})
	}); err != nil {
		return err
	}

	// Say so when there is more than they are seeing. Silently truncating reads
	// as "this is everything", which for a status report is worse than showing
	// nothing.
	if page.Meta.Total > len(page.Projects) {
		deps.Out.Notice("Showing %d of %d. Use --limit to see more.", len(page.Projects), page.Meta.Total)
	}

	return nil
}

func newProjectsShowCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one project",
		// At most one, so a bare `projects show` keeps its own question.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fail.UsageHint("Which project?", "Pass its id: basa projects show <id> --env <name>")
			}

			return runProjectsShow(cmd.Context(), deps, args[0])
		},
	}
}

func runProjectsShow(ctx context.Context, deps *Deps, id string) error {
	c, env, err := clientFor(deps)
	if err != nil {
		return err
	}

	team, err := resolveTeam(ctx, deps, c)
	if err != nil {
		return err
	}

	if deps.Out.JSON {
		raw, err := c.ProjectRaw(ctx, team.ID, id)
		if err != nil {
			return err
		}
		return deps.Out.Data(raw, nil)
	}

	project, err := c.Project(ctx, team.ID, id)
	if err != nil {
		return err
	}

	return deps.Out.Data(nil, func(io.Writer) error {
		return deps.Out.Record(output.Record{Fields: []output.Field{
			{Key: "Environment", Value: env},
			{Key: "Team", Value: team.Name},
			{Key: "Project", Value: project.ID},
			{Key: "Name", Value: project.Name},
			// Frequently null, and its absence is meaningful: talent then sees
			// the internal name. "not set" says that; a dash would read as data
			// the server failed to send.
			{Key: "Name talent sees", Value: derefOr(project.ExternalName, "not set")},
			{Key: "Brand", Value: projectBrand(*project)},
			{Key: "Type", Value: derefOr(project.Type, "—")},
			{Key: "NDA required", Value: yesNo(project.NDARequired)},
			{Key: "Archived", Value: yesNo(project.Archived)},
			{Key: "Created", Value: shortDate(project.CreatedAt)},
			{Key: "Updated", Value: shortDate(project.UpdatedAt)},
		}})
	})
}

// --- formatting ------------------------------------------------------------

func projectBrand(p client.Project) string {
	if p.Brand == nil {
		return "—"
	}
	return p.Brand.Name
}

// uniformBrand reports the one brand every project on the page belongs to, when
// there is exactly one. Matched on name because the payload carries no brand id
// — two distinct brands sharing a name would collapse into one heading, which
// is the same thing the operator would see in the column anyway.
func uniformBrand(projects []client.Project) (string, bool) {
	if len(projects) == 0 || projects[0].Brand == nil {
		return "", false
	}
	name := projects[0].Brand.Name
	if name == "" {
		return "", false
	}
	for _, p := range projects[1:] {
		if p.Brand == nil || p.Brand.Name != name {
			return "", false
		}
	}
	return name, true
}

// archivedSpread reports whether the page holds both archived and active
// projects and, when it does not, which of the two they all are.
func archivedSpread(projects []client.Project) (mixed, allArchived bool) {
	if len(projects) == 0 {
		return false, false
	}
	first := projects[0].Archived
	for _, p := range projects[1:] {
		if p.Archived != first {
			return true, false
		}
	}
	return false, first
}

// yesNo renders a boolean the server actually reported. Unlike an absent
// string this is never rendered as an em dash: "no" is a known answer, and a
// dash would read as missing data.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
