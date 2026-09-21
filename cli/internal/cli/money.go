package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	waffle "github.com/very-good-labs/waffle-go"
)

// moneyClient builds the waffle-go client for the resolved profile.
// Only the API key is attached — every command in this file talks to
// the money-moving surface, where a dashboard session token is
// meaningless (the contract forbids it from creating charges/payouts).
func (r *resolved) moneyClient() (*waffle.Client, error) {
	key, err := r.requireAPIKey()
	if err != nil {
		return nil, err
	}
	return waffle.NewClient(key, waffle.WithBaseURL(r.apiBase())), nil
}

func newBalanceCmd() *cobra.Command {
	var currency string
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Show your withdrawable balance",
		Long: `Show the withdrawable balance (GET /v1/balance): settled paid
charges minus non-failed payouts. A charge inside its settlement hold
window does not count yet even if its status is "paid" — there is no
separate pending-settlement endpoint.`,
		Example: `  waffle balance
  waffle balance --currency IDR`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			c, err := r.moneyClient()
			if err != nil {
				return err
			}
			bal, err := c.GetBalance(cmd.Context(), currency)
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, bal)
			}
			printKV(os.Stdout, "", []kv{
				row("Balance", money(bal.Amount, bal.Currency)),
				row("Currency", bal.Currency),
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&currency, "currency", "", "currency code (server defaults to IDR)")
	return cmd
}

func newBanksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "banks",
		Short: "List the active bank directory",
		Long: `List waffle's active bank directory (GET /v1/banks): the banks
usable for virtual accounts and, for payouts, the bank_code values
accepted by "waffle bank-accounts register". Unauthenticated on the
server; ordered by sort_order.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List banks",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			// ListBanks is public server-side; the stored key (if any)
			// rides along harmlessly.
			c, keyErr := r.moneyClient()
			if keyErr != nil {
				c = waffle.NewClient("", waffle.WithBaseURL(r.apiBase()))
			}
			banks, err := c.ListBanks(cmd.Context())
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, banks)
			}
			rows := make([][]string, 0, len(banks))
			for _, b := range banks {
				rows = append(rows, []string{b.Code, b.Name, strconv.Itoa(b.SortOrder)})
			}
			printTable(os.Stdout, []string{"CODE", "NAME", "SORT"}, rows)
			return nil
		},
	})
	return cmd
}

func newChargesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "charges",
		Short: "Create charges and preview fees",
	}
	cmd.AddCommand(newChargesCreateCmd(), newChargesCalculateFeeCmd())
	return cmd
}

// parseMetadata consumes --metadata key=value repetitions.
func parseMetadata(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid --metadata %q: expected key=value", p)
		}
		out[k] = v
	}
	return out, nil
}

func newChargesCreateCmd() *cobra.Command {
	var (
		amount     int64
		currency   string
		desc       string
		channel    string
		vaBank     string
		customer   string
		returnURL  string
		expiresMin int
		metadata   []string
		idemKey    string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a charge (POST /v1/charges)",
		Long: `Create a charge. Amounts are integers in the currency's minor
unit — IDR has no minor unit in waffle, so 12500 means Rp12.500.

Without --channel the response carries a hosted checkout_url. With
--channel qris it carries the raw QR string; with --channel
virtual_account it carries a VA number (--va-bank required).

An Idempotency-Key is required by the contract: pass your own
(--idempotency-key, e.g. your internal order id, so retries are safe)
or one is generated for this invocation.`,
		Example: `  waffle charges create --amount 100000
  waffle charges create --amount 100000 --channel qris --description "Order 42"
  waffle charges create --amount 100000 --channel virtual_account --va-bank BCA
  waffle charges create --amount 100000 --metadata order=42 --metadata plan=pro`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if amount <= 0 {
				return fmt.Errorf("--amount must be a positive integer (minor unit)")
			}
			if currency == "" {
				return fmt.Errorf("--currency is required (e.g. IDR)")
			}
			switch channel {
			case "", "qris", "virtual_account":
			default:
				return fmt.Errorf("invalid --channel %q: must be qris or virtual_account (empty = hosted checkout)", channel)
			}
			if channel == "virtual_account" && vaBank == "" {
				return fmt.Errorf("--va-bank is required with --channel virtual_account")
			}
			meta, err := parseMetadata(metadata)
			if err != nil {
				return err
			}
			r, err := g.load()
			if err != nil {
				return err
			}
			c, err := r.moneyClient()
			if err != nil {
				return err
			}
			if idemKey == "" {
				idemKey = waffle.GenerateIdempotencyKey()
			}
			charge, err := c.CreateCharge(cmd.Context(), waffle.CreateChargeParams{
				Amount:           amount,
				Currency:         currency,
				Description:      desc,
				CustomerRef:      customer,
				ReturnURL:        returnURL,
				Channel:          channel,
				VABank:           vaBank,
				ExpiresInMinutes: expiresMin,
				Metadata:         meta,
			}, idemKey)
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, charge)
			}
			rows := []kv{
				row("Charge", charge.ID),
				row("Status", charge.Status),
				row("Amount", money(charge.GrossAmount, charge.Currency)),
				row("Fee", money(charge.FeeAmount, charge.Currency)),
				row("Net", money(charge.NetAmount, charge.Currency)),
			}
			switch {
			case charge.QRString != "":
				rows = append(rows, row("QR", charge.QRString))
			case charge.VANumber != "":
				rows = append(rows, row("VA bank", charge.VABank), row("VA number", charge.VANumber))
			case charge.CheckoutURL != "":
				rows = append(rows, row("Checkout", charge.CheckoutURL))
			}
			printKV(os.Stdout, "Charge created.", rows)
			fmt.Fprintf(os.Stdout, "\nIdempotency key: %s (reuse it to retry this exact charge safely)\n", idemKey)
			return nil
		},
	}
	cmd.Flags().Int64Var(&amount, "amount", 0, "amount, integer minor unit (required)")
	cmd.Flags().StringVar(&currency, "currency", "IDR", "currency code")
	cmd.Flags().StringVar(&desc, "description", "", "human-readable description")
	cmd.Flags().StringVar(&channel, "channel", "", "qris | virtual_account (empty = hosted checkout)")
	cmd.Flags().StringVar(&vaBank, "va-bank", "", "bank code for virtual_account (see waffle banks list)")
	cmd.Flags().StringVar(&customer, "customer-ref", "", "your customer reference")
	cmd.Flags().StringVar(&returnURL, "return-url", "", "URL the payer returns to after checkout")
	cmd.Flags().IntVar(&expiresMin, "expires-in-minutes", 0, "charge expiry in minutes")
	cmd.Flags().StringArrayVar(&metadata, "metadata", nil, "metadata as key=value (repeatable)")
	cmd.Flags().StringVar(&idemKey, "idempotency-key", "", "reuse across retries to guarantee a single charge (auto-generated when omitted)")
	return cmd
}

func newChargesCalculateFeeCmd() *cobra.Command {
	var (
		amount   int64
		currency string
		channel  string
		vaBank   string
	)
	cmd := &cobra.Command{
		Use:   "calculate-fee",
		Short: "Preview the fee for an amount without creating a charge",
		Long: `Preview-only fee quote (POST /v1/fees/calculate): no charge, no
ledger write, no provider call. Resolves against the same auto-routed
provider a real charge would use, so the quote matches the bill.`,
		Example: `  waffle charges calculate-fee --amount 100000
  waffle charges calculate-fee --amount 100000 --channel qris
  waffle charges calculate-fee --amount 100000 --channel virtual_account --va-bank BCA`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if amount <= 0 {
				return fmt.Errorf("--amount must be a positive integer (minor unit)")
			}
			if currency == "" {
				return fmt.Errorf("--currency is required (e.g. IDR)")
			}
			r, err := g.load()
			if err != nil {
				return err
			}
			c, err := r.moneyClient()
			if err != nil {
				return err
			}
			quote, err := c.CalculateFee(cmd.Context(), waffle.CalculateFeeParams{
				Amount:   amount,
				Currency: currency,
				Channel:  channel,
				VABank:   vaBank,
			})
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, quote)
			}
			printKV(os.Stdout, "Fee quote (preview only).", []kv{
				row("Gross", money(quote.GrossAmount, quote.Currency)),
				row("Fee", money(quote.FeeAmount, quote.Currency)),
				row("Net", money(quote.NetAmount, quote.Currency)),
			})
			return nil
		},
	}
	cmd.Flags().Int64Var(&amount, "amount", 0, "amount, integer minor unit (required)")
	cmd.Flags().StringVar(&currency, "currency", "IDR", "currency code")
	cmd.Flags().StringVar(&channel, "channel", "", "narrow the quote: qris | virtual_account")
	cmd.Flags().StringVar(&vaBank, "va-bank", "", "narrow a virtual_account quote to one bank")
	return cmd
}

func newBankAccountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bank-accounts",
		Short: "Register and inspect your withdrawal bank account",
	}
	cmd.AddCommand(newBankAccountsRegisterCmd(), newBankAccountsGetCmd())
	return cmd
}

func newBankAccountsRegisterCmd() *cobra.Command {
	var (
		bankCode string
		acctNo   string
		holder   string
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register the active withdrawal account",
		Long: `Register a withdrawal destination (POST /v1/bank-accounts).
Registering a new account disables the previous active one — only one
active withdrawal destination per mode at a time. A payout drawn
against a freshly-registered account is held (not dispatched) for a
security window, then resumed automatically.`,
		Example: `  waffle bank-accounts register --bank-code BCA --account-number 1234567890 --account-holder-name "Budi Santoso"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bankCode == "" || acctNo == "" || holder == "" {
				return fmt.Errorf("--bank-code, --account-number and --account-holder-name are all required")
			}
			r, err := g.load()
			if err != nil {
				return err
			}
			c, err := r.moneyClient()
			if err != nil {
				return err
			}
			acct, err := c.RegisterBankAccount(cmd.Context(), waffle.RegisterBankAccountParams{
				BankCode:          bankCode,
				AccountNumber:     acctNo,
				AccountHolderName: holder,
			})
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, acct)
			}
			printKV(os.Stdout, "Bank account registered (now the active withdrawal destination).", []kv{
				row("ID", acct.ID),
				row("Bank", acct.BankCode),
				row("Account", acct.AccountNumber),
				row("Holder", acct.AccountHolderName),
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&bankCode, "bank-code", "", "bank code from waffle banks list (required)")
	cmd.Flags().StringVar(&acctNo, "account-number", "", "bank account number (required)")
	cmd.Flags().StringVar(&holder, "account-holder-name", "", "account holder name (required)")
	return cmd
}

func newBankAccountsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the current active withdrawal account",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			c, err := r.moneyClient()
			if err != nil {
				return err
			}
			acct, err := c.GetActiveBankAccount(cmd.Context())
			if err != nil {
				var apiErr *waffle.APIError
				if g.jsonOut && errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
					return emitJSON(os.Stdout, struct {
						Active bool   `json:"active"`
						Error  string `json:"error"`
					}{false, apiErr.Message})
				}
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, acct)
			}
			printKV(os.Stdout, "", []kv{
				row("ID", acct.ID),
				row("Bank", acct.BankCode),
				row("Account", acct.AccountNumber),
				row("Holder", acct.AccountHolderName),
				row("Active since", acct.CreatedAt),
			})
			return nil
		},
	}
}

func newPayoutsCmd() *cobra.Command {
	var (
		bankAcctID string
		amount     int64
		currency   string
		idemKey    string
	)
	cmd := &cobra.Command{
		Use:   "payouts",
		Short: "Withdraw balance to your bank account",
	}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a payout (POST /v1/payouts)",
		Long: `Withdraw to a registered bank account. Amounts are integers in the
currency's minor unit. An Idempotency-Key is required: pass your own or
one is generated for this invocation.

Status "held" means the payout was claimed (debited) but drawn against
a bank account registered inside the security window — it resumes
automatically, no action needed.`,
		Example: `  waffle payouts create --bank-account-id <uuid> --amount 20000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bankAcctID == "" {
				return fmt.Errorf("--bank-account-id is required (see waffle bank-accounts get)")
			}
			if amount <= 0 {
				return fmt.Errorf("--amount must be a positive integer (minor unit)")
			}
			if currency == "" {
				return fmt.Errorf("--currency is required (e.g. IDR)")
			}
			r, err := g.load()
			if err != nil {
				return err
			}
			c, err := r.moneyClient()
			if err != nil {
				return err
			}
			if idemKey == "" {
				idemKey = waffle.GenerateIdempotencyKey()
			}
			p, err := c.CreatePayout(cmd.Context(), waffle.CreatePayoutParams{
				BankAccountID: bankAcctID,
				Amount:        amount,
				Currency:      currency,
			}, idemKey)
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, p)
			}
			rows := []kv{
				row("Payout", p.ID),
				row("Status", p.Status),
				row("Amount", money(p.Amount, p.Currency)),
				row("Bank account", p.BankAccountID),
			}
			if p.Status == "held" {
				rows = append(rows, row("Note", "security hold on the fresh bank account — resumes automatically"))
			}
			printKV(os.Stdout, "Payout created.", rows)
			fmt.Fprintf(os.Stdout, "\nIdempotency key: %s\n", idemKey)
			return nil
		},
	}
	create.Flags().StringVar(&bankAcctID, "bank-account-id", "", "registered bank account id (required)")
	create.Flags().Int64Var(&amount, "amount", 0, "amount, integer minor unit (required)")
	create.Flags().StringVar(&currency, "currency", "IDR", "currency code")
	create.Flags().StringVar(&idemKey, "idempotency-key", "", "reuse across retries to guarantee a single payout (auto-generated when omitted)")
	cmd.AddCommand(create)
	return cmd
}

func newHealthzCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthz",
		Short: "Check that the merchant API is up (no auth)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := g.load()
			if err != nil {
				return err
			}
			// Healthz takes no credential; construct without resolving a key.
			c := waffle.NewClient("", waffle.WithBaseURL(r.apiBase()))
			if err := c.Healthz(cmd.Context()); err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, map[string]string{"status": "ok", "base_url": r.apiBase()})
			}
			fmt.Fprintf(os.Stdout, "ok (%s)\n", r.apiBase())
			return nil
		},
	}
}
