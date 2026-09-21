package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getwaffle/sdk/cli/internal/session"
)

func newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "List, create, and revoke your sandbox API keys",
		Long: `Manage the API keys that authenticate money-moving calls.

List/create/revoke use the dashboard session from "waffle login".
Self-service minting is sandbox-only by design (live keys are
admin-issued after review). "keys create" stores the fresh key in the
config file automatically so money commands work immediately.`,
	}
	cmd.AddCommand(newKeysListCmd(), newKeysCreateCmd(), newKeysRevokeCmd())
	return cmd
}

func newKeysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your API keys (revoked included, plaintext never shown)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			token, err := r.requireSession()
			if err != nil {
				return err
			}
			keys, err := session.New(r.apiBase(), token).ListAPIKeys(cmd.Context())
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, keys)
			}
			if len(keys) == 0 {
				fmt.Fprintln(os.Stdout, "No API keys yet — create one: waffle keys create")
				return nil
			}
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				name := ""
				if k.Name != nil {
					name = *k.Name
				}
				revoked := ""
				if k.RevokedAt != nil {
					revoked = *k.RevokedAt
				}
				rows = append(rows, []string{k.ID, k.Mode, k.Prefix + "...", orDash(name), k.CreatedAt, orDash(revoked)})
			}
			printTable(os.Stdout, []string{"ID", "MODE", "PREFIX", "NAME", "CREATED", "REVOKED"}, rows)
			return nil
		},
	}
}

func newKeysCreateCmd() *cobra.Command {
	var (
		name    string
		noStore bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Mint a new sandbox API key and store it",
		Long: `Mint a new sandbox API key (POST /v1/merchants/me/api-keys).

The plaintext key is shown exactly once, here, and stored in the config
file so balance/charges/payouts work immediately. Pass --no-store to
skip storing it (e.g. when minting for another machine).`,
		Example: `  waffle keys create --name "laptop"
  waffle keys create --name "staging box" --no-store`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			token, err := r.requireSession()
			if err != nil {
				return err
			}
			key, err := session.New(r.apiBase(), token).CreateAPIKey(cmd.Context(), name)
			if err != nil {
				return err
			}
			if !noStore {
				r.prof.APIKey = key.APIKey
				if err := r.cfg.Save(); err != nil {
					return err
				}
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, key)
			}
			printKV(os.Stdout, "Sandbox API key created.", []kv{
				row("Key", key.APIKey),
				row("Mode", key.Mode),
			})
			if noStore {
				fmt.Fprintln(os.Stdout, "(not stored — copy it now, it is never shown again)")
			} else {
				fmt.Fprintf(os.Stdout, "Stored in %s — money commands are ready to use.\n", mustConfigPath())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "optional label to tell keys apart (e.g. \"staging box\")")
	cmd.Flags().BoolVar(&noStore, "no-store", false, "do not store the new key in the config file")
	return cmd
}

func newKeysRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <key-id>",
		Short:   "Revoke one of your API keys",
		Args:    cobra.ExactArgs(1),
		Example: `  waffle keys revoke 0d9a6c1e-...`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			token, err := r.requireSession()
			if err != nil {
				return err
			}
			id := strings.TrimSpace(args[0])
			if err := session.New(r.apiBase(), token).RevokeAPIKey(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "Key %s revoked.\n", id)
			return nil
		},
	}
}
