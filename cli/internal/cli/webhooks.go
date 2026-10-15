package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	waffle "github.com/getwaffle/sdk/go"
	"github.com/spf13/cobra"
)

const envWebhookSecret = "WAFFLE_WEBHOOK_SECRET"

func newWebhooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhooks",
		Short: "Work with webhook deliveries sent to your endpoint",
	}
	cmd.AddCommand(newWebhooksVerifyCmd())
	return cmd
}

// newWebhooksVerifyCmd runs waffle.VerifyWebhook offline against a
// captured delivery, so a receiver's signing code can be debugged
// without network access or any API credential. The secret is read
// from WAFFLE_WEBHOOK_SECRET or --secret-file — deliberately not a
// flag, because argv is visible to other local users.
func newWebhooksVerifyCmd() *cobra.Command {
	var (
		bodyFile   string
		secretFile string
		headers    []string
		tolerance  time.Duration
		noLegacy   bool
		atUnix     int64
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify a captured webhook delivery's signature offline",
		Long: `Verify the signature of a captured webhook delivery and print the event.

The raw request body must be byte-exact (a re-formatted JSON body will
not verify). Pass the headers Waffle sent with --header; X-Waffle-
Signature-V2 is verified when present, otherwise the legacy
X-Waffle-Signature (no replay protection) unless --no-legacy is set.

The signing secret is read from the WAFFLE_WEBHOOK_SECRET environment
variable, or from --secret-file. It is never accepted as a flag.

Because captured deliveries age, V2 timestamps are checked against the
current time; use --at <unix> to verify as of the time it was received.
Exits non-zero with the failure reason when verification fails.`,
		Example: `  waffle webhooks verify --body-file body.json \
    --header 'X-Waffle-Signature-V2: t=1767323045,v1=2018c5...' \
    --header 'X-Waffle-Delivery: d1b0c3a2-...' --at 1767323045`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bodyFile == "" {
				return fmt.Errorf("--body-file is required (use - for stdin)")
			}
			secret, err := readWebhookSecret(secretFile)
			if err != nil {
				return err
			}
			var body []byte
			if bodyFile == "-" {
				body, err = io.ReadAll(stdin)
			} else {
				body, err = os.ReadFile(bodyFile)
			}
			if err != nil {
				return fmt.Errorf("read body: %w", err)
			}
			h := http.Header{}
			for _, line := range headers {
				name, val, ok := strings.Cut(line, ":")
				if !ok || strings.TrimSpace(name) == "" {
					return fmt.Errorf("invalid --header %q: expected 'Name: value'", line)
				}
				h.Add(strings.TrimSpace(name), strings.TrimSpace(val))
			}
			opts := []waffle.WebhookOption{
				waffle.WithWebhookTolerance(tolerance),
				waffle.WithWebhookAllowLegacy(!noLegacy),
			}
			if atUnix != 0 {
				at := time.Unix(atUnix, 0)
				opts = append(opts, waffle.WithWebhookClock(func() time.Time { return at }))
			}
			ev, err := waffle.VerifyWebhook(body, h, secret, opts...)
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(os.Stdout, map[string]any{
					"verified":    true,
					"delivery_id": ev.DeliveryID,
					"event":       ev.Raw,
				})
			}
			printKV(os.Stdout, "Signature valid", []kv{
				row("Event", ev.Event),
				row("Event ID", ev.EventID),
				row("Delivery", ev.DeliveryID),
				row("Charge", ev.ChargeID),
				row("Payout", ev.PayoutID),
				row("Status", ev.Status),
				row("Amount", moneyOrEmpty(ev)),
				row("Mode", ev.Mode),
				row("Created", ev.CreatedAt),
			})
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&bodyFile, "body-file", "", "file holding the exact raw request body (- for stdin)")
	f.StringVar(&secretFile, "secret-file", "", "file holding the signing secret (default: $"+envWebhookSecret+")")
	f.StringArrayVar(&headers, "header", nil, "request header as 'Name: value' (repeatable)")
	f.DurationVar(&tolerance, "tolerance", waffle.DefaultWebhookTolerance, "max age/skew of the V2 timestamp")
	f.BoolVar(&noLegacy, "no-legacy", false, "reject deliveries that only carry the legacy X-Waffle-Signature")
	f.Int64Var(&atUnix, "at", 0, "verify the timestamp as of this unix time instead of now")
	return cmd
}

func moneyOrEmpty(ev *waffle.WebhookEvent) string {
	if _, ok := ev.Raw["amount"]; !ok {
		return ""
	}
	return fmt.Sprintf("%d %s", ev.Amount, ev.Currency)
}

func readWebhookSecret(file string) (string, error) {
	var secret string
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read secret file: %w", err)
		}
		secret = strings.TrimSpace(string(b))
	} else {
		secret = os.Getenv(envWebhookSecret)
	}
	if secret == "" {
		return "", fmt.Errorf("no signing secret: set %s or pass --secret-file", envWebhookSecret)
	}
	return secret, nil
}
