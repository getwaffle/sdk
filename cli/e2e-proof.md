# paybridge CLI — E2E proof transcript

Local stack: `make start` + `make seed` (api :8080, admin :8081, worker,
mockgdc :8095, notifier, postgres). Binary: `go build ./sdk/cli` (actual
built binary, not `go run`). Merchant registered via the same flow as
`scripts/demo-e2e.sh` (register -> DB-verify email -> CLI login ->
KYC -> admin review auto-connects PSPs).

## Login (email+password piped; TTY prompts are no-echo)

    $ paybridge-cli login --email cli-e2e-1789647452@gmail.com
    Password:
    Logged in. Session stored in .e2e-config/paybridge/config.json.
      Logged in:  cli-e2e-1789647452@gmail.com
      Profile:   default
      Session:   e2dba0b13627...

    No API key stored yet — money commands (balance, charges, payouts) need one.
      paybridge keys create            mint + store a sandbox key right here

## whoami (session token -> /v1/merchants/me/profile)

    $ paybridge-cli whoami
      Merchant ID:  08114e2a-2d5e-4f0c-81b5-95692854d784
      Email:       cli-e2e-1789647452@gmail.com
      Status:      pending
      Review:      pending
      Entity:      company
      Role:        owner
      Created:     2026-09-17T19:17:32+07:00

## keys create (session -> mint sandbox key -> auto-stored)

    $ paybridge-cli keys create --name e2e-cli
    Sandbox API key created.
      Key:  pb_sandbox_4f43baa5f65ff5de420578a253e1b1e38157202427e67057
      Mode:  sandbox
    Stored in .e2e-config/paybridge/config.json — money commands are ready to use.

## charges create --channel qris (API key -> POST /v1/charges)

    $ paybridge-cli charges create --amount 50000 --channel qris --description "cli-e2e order 1"
    Charge created.
      Charge:  e310fce1-ed87-4731-85a0-4704a14ed05c
      Status:  pending
      Amount:  Rp50.000
      Fee:    Rp640
      Net:    Rp49.360
      QR:     00020101021226610014ID.CO.QRIS.WWW2d7f3ece...

    Idempotency key: 4d31f54e9165104c9ffcc54d09b7a4d4 (reuse it to retry this exact charge safely)

Settled on mockgdc (`POST /mock/simulate outcome=paid`, same signed push
as demo-e2e.sh), then:

    $ paybridge-cli balance
      Balance:  Rp49.360
      Currency:  IDR

## charges calculate-fee (channel/bank-narrowed quote)

    $ paybridge-cli charges calculate-fee --amount 100000 --channel virtual_account --va-bank BCA
    Fee quote (preview only).
      Gross:  Rp100.000
      Fee:   Rp4.500
      Net:   Rp95.500

## bank-accounts + payouts

    $ paybridge-cli bank-accounts register --bank-code BCA --account-number 1234567890 --account-holder-name "E2E Owner"
    Bank account registered (now the active withdrawal destination).
      ID:      8e3ea2ab-8a05-45da-a30c-e68ba200a60b
      ...

    $ paybridge-cli payouts create --bank-account-id 8e3ea2ab-... --amount 20000
    Payout created.
      Payout:       2dca44dd-1110-4a18-a097-2a467359e5e9
      Status:       held
      Amount:       Rp20.000
      Bank account: 8e3ea2ab-...
      Note:         security hold on the fresh bank account — resumes automatically

After the local 10s hold window (`PAYOUT_HOLD_WINDOW_SECONDS=10`), the
worker auto-resumed the payout and the balance dropped accordingly:

    $ paybridge-cli balance
      Balance:  Rp29.360      # 49.360 - 20.000

## JSON mode + logout + auth guard

    $ paybridge-cli whoami --json
    {
      "merchant_id": "08114e2a-...",
      "business_name": "CLI E2E Warung",
      ...
    }

    $ paybridge-cli logout
    Profile "default": session and API key cleared.

    $ paybridge-cli whoami
    error: not logged in — run "paybridge login" first        (exit 1)

## Backend wiring fix found by this E2E

`GET /v1/merchants/me/profile` panicked (`nil *RegistrationRepository`)
on the freshly built api binary: `cmd/api/main.go` constructed
`registrationRepo` but never set `Profiles:` in the `httpapi.Services`
literal (tests set it; production wiring omitted it). One-line wiring
fix included in this PR — nothing else in the backend was touched.
