package commands

import (
	"context"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Basa-Futura/basa-cli/internal/client"
	"github.com/Basa-Futura/basa-cli/internal/fail"
	"github.com/Basa-Futura/basa-cli/internal/output"
)

// NewContractsCmd builds the `basa contracts` group.
func NewContractsCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "contracts",
		Aliases: []string{"contract"},
		Short:   "List and show contracts",
		Long: `List and show contracts.

"What is awaiting signature" is --status ready_for_signature.`,
		Example: `  basa contracts list --env staging
  basa contracts list --env staging --status ready_for_signature
  basa contracts show Qp7Wn --env staging`,
	}

	cmd.AddCommand(newContractsListCmd(deps), newContractsShowCmd(deps))

	return cmd
}

func newContractsListCmd(deps *Deps) *cobra.Command {
	var (
		status string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the team's contracts",
		Args:  rejectStrayArgs("contracts list", "contracts show <id>"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			filters := client.ContractFilters{Status: status}
			// See the same guard on deals list: a typed --limit 0 belongs to
			// the server's validation, not to this client's default.
			if cmd.Flags().Changed("limit") {
				filters.Limit = &limit
			}

			return runContractsList(cmd.Context(), deps, filters)
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "Only this status (draft, ready_for_signature, signed, declined, voided)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "How many to show (1-100, default 25)")

	return cmd
}

func runContractsList(ctx context.Context, deps *Deps, filters client.ContractFilters) error {
	c, env, err := clientFor(deps)
	if err != nil {
		return err
	}

	team, err := resolveTeam(ctx, deps, c)
	if err != nil {
		return err
	}

	if deps.Out.JSON {
		raw, err := c.ContractsRaw(ctx, team.ID, filters)
		if err != nil {
			return err
		}
		return deps.Out.Data(raw, nil)
	}

	page, err := c.Contracts(ctx, team.ID, filters)
	if err != nil {
		return err
	}

	deps.Out.Notice("%s · %s", env, team.Name)

	if len(page.Contracts) == 0 {
		deps.Out.Notice("No contracts matched.")

		return nil
	}

	rows := make([][]string, 0, len(page.Contracts))
	for _, contract := range page.Contracts {
		rows = append(rows, []string{
			contract.ID,
			contractStatusLabel(contract),
			derefOr(contract.Name, "—"),
			derefOr(contract.Recipient, "—"),
			contractDealID(contract),
			shortDate(contract.UpdatedAt),
		})
	}

	if err := deps.Out.Data(nil, func(io.Writer) error {
		return deps.Out.Table(output.Table{
			Headers: []string{"ID", "STATUS", "NAME", "RECIPIENT", "DEAL", "UPDATED"},
			Rows:    rows,
		})
	}); err != nil {
		return err
	}

	if page.Meta.Total > len(page.Contracts) {
		deps.Out.Notice("Showing %d of %d. Use --limit to see more.", len(page.Contracts), page.Meta.Total)
	}

	return nil
}

func newContractsShowCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one contract",
		// At most one, so a bare `contracts show` keeps its own question.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fail.UsageHint("Which contract?", "Pass its id: basa contracts show <id> --env <name>")
			}

			return runContractsShow(cmd.Context(), deps, args[0])
		},
	}
}

func runContractsShow(ctx context.Context, deps *Deps, id string) error {
	c, env, err := clientFor(deps)
	if err != nil {
		return err
	}

	team, err := resolveTeam(ctx, deps, c)
	if err != nil {
		return err
	}

	if deps.Out.JSON {
		raw, err := c.ContractRaw(ctx, team.ID, id)
		if err != nil {
			return err
		}
		return deps.Out.Data(raw, nil)
	}

	contract, err := c.Contract(ctx, team.ID, id)
	if err != nil {
		return err
	}

	return deps.Out.Data(nil, func(io.Writer) error {
		return deps.Out.Record(output.Record{Fields: []output.Field{
			{Key: "Environment", Value: env},
			{Key: "Team", Value: team.Name},
			{Key: "Contract", Value: contract.ID},
			{Key: "Name", Value: derefOr(contract.Name, "—")},
			{Key: "Status", Value: contractStatusLabel(*contract)},
			{Key: "Version", Value: strconv.Itoa(contract.Sequence)},
			{Key: "Recipient", Value: derefOr(contract.Recipient, "—")},
			{Key: "Deal", Value: contractDealID(*contract)},
			{Key: "Signed", Value: shortDate(contract.SignedAt)},
			{Key: "Declined", Value: shortDate(contract.DeclinedAt)},
			{Key: "Updated", Value: shortDate(contract.UpdatedAt)},
		}})
	})
}

// --- formatting ------------------------------------------------------------

func contractStatusLabel(c client.Contract) string {
	if c.Status == nil {
		return "—"
	}
	return c.Status.Label
}

// contractDealID reports "standalone" rather than an em dash when there is no
// deal. A contract without one is a normal, meaningful shape, and a dash would
// read as missing data.
func contractDealID(c client.Contract) string {
	if c.Deal == nil {
		return "standalone"
	}
	return c.Deal.ID
}
