package waffle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient spins up an httptest.Server whose handler is provided by
// the caller and returns a *Client pointed at it, plus the server for
// cleanup.
func newTestClient(t *testing.T, apiKey string, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(apiKey, WithBaseURL(srv.URL))
	return c, srv
}

func TestCreateCharge(t *testing.T) {
	tests := []struct {
		name           string
		params         CreateChargeParams
		idempotencyKey string
		wantErr        bool
	}{
		{
			name: "basic charge",
			params: CreateChargeParams{
				Amount:   100000,
				Currency: "IDR",
				Metadata: map[string]string{"order": "abc"},
			},
			idempotencyKey: "idem-key-1",
		},
		{
			name: "another charge",
			params: CreateChargeParams{
				Amount:   50000,
				Currency: "IDR",
			},
			idempotencyKey: "idem-key-2",
		},
		{
			name: "missing idempotency key",
			params: CreateChargeParams{
				Amount:   50000,
				Currency: "IDR",
			},
			idempotencyKey: "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotAuth, gotIdemKey string
			var gotBody map[string]any
			c, _ := newTestClient(t, "sk_test_123", func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				gotIdemKey = r.Header.Get("Idempotency-Key")
				_ = json.NewDecoder(r.Body).Decode(&gotBody)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				resp := Charge{
					ID:          "chg_1",
					Mode:        "sandbox",
					Status:      "pending",
					GrossAmount: tt.params.Amount,
					FeeAmount:   3000,
					NetAmount:   tt.params.Amount - 3000,
					Currency:    tt.params.Currency,
					CreatedAt:   "2026-09-08T01:00:00Z",
				}
				_ = json.NewEncoder(w).Encode(resp)
			})

			charge, err := c.CreateCharge(context.Background(), tt.params, tt.idempotencyKey)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != "/v1/charges" {
				t.Errorf("path = %q, want /v1/charges", gotPath)
			}
			if gotAuth != "Bearer sk_test_123" {
				t.Errorf("Authorization = %q", gotAuth)
			}
			if gotIdemKey != tt.idempotencyKey {
				t.Errorf("Idempotency-Key = %q, want %q", gotIdemKey, tt.idempotencyKey)
			}
			if _, ok := gotBody["provider"]; ok {
				t.Errorf("expected no provider field in request body, got %v", gotBody["provider"])
			}
			if int64(gotBody["amount"].(float64)) != tt.params.Amount {
				t.Errorf("body amount = %v, want %v", gotBody["amount"], tt.params.Amount)
			}
			if charge.GrossAmount != tt.params.Amount {
				t.Errorf("GrossAmount = %d, want %d", charge.GrossAmount, tt.params.Amount)
			}
		})
	}
}

func TestCreateChargeWithChannel(t *testing.T) {
	var gotBody map[string]any
	c, _ := newTestClient(t, "sk_test_123", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := Charge{
			ID:          "chg_va_1",
			Mode:        "sandbox",
			Status:      "pending",
			GrossAmount: 75000,
			FeeAmount:   2000,
			NetAmount:   73000,
			Currency:    "IDR",
			Channel:     "virtual_account",
			VABank:      "BCA",
			VANumber:    "8808123456789",
			CreatedAt:   "2026-09-08T01:00:00Z",
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	charge, err := c.CreateCharge(context.Background(), CreateChargeParams{
		Amount:   75000,
		Currency: "IDR",
		Channel:  "virtual_account",
		VABank:   "BCA",
	}, "idem-key-va-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody["channel"] != "virtual_account" {
		t.Errorf("body channel = %v, want %q", gotBody["channel"], "virtual_account")
	}
	if gotBody["va_bank"] != "BCA" {
		t.Errorf("body va_bank = %v, want %q", gotBody["va_bank"], "BCA")
	}
	if _, ok := gotBody["qr_string"]; ok {
		t.Errorf("expected qr_string omitted from request body, got %v", gotBody["qr_string"])
	}

	if charge.Channel != "virtual_account" {
		t.Errorf("Channel = %q, want %q", charge.Channel, "virtual_account")
	}
	if charge.VABank != "BCA" {
		t.Errorf("VABank = %q, want %q", charge.VABank, "BCA")
	}
	if charge.VANumber != "8808123456789" {
		t.Errorf("VANumber = %q, want %q", charge.VANumber, "8808123456789")
	}
	if charge.CheckoutURL != "" {
		t.Errorf("CheckoutURL = %q, want empty for channel checkout", charge.CheckoutURL)
	}
}

func TestCreateChargeQRISResponseDeserialization(t *testing.T) {
	c, _ := newTestClient(t, "sk_test_123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "chg_qris_1",
			"mode": "sandbox",
			"status": "pending",
			"gross_amount": 15000,
			"fee_amount": 500,
			"net_amount": 14500,
			"currency": "IDR",
			"channel": "qris",
			"qr_string": "00020101021226610014ID.CO.QRIS.WWW",
			"created_at": "2026-09-08T01:00:00Z"
		}`))
	})

	charge, err := c.CreateCharge(context.Background(), CreateChargeParams{
		Amount:   15000,
		Currency: "IDR",
		Channel:  "qris",
	}, "idem-key-qris-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if charge.Channel != "qris" {
		t.Errorf("Channel = %q, want %q", charge.Channel, "qris")
	}
	if charge.QRString != "00020101021226610014ID.CO.QRIS.WWW" {
		t.Errorf("QRString = %q, want the QRIS payload", charge.QRString)
	}
	if charge.VABank != "" || charge.VANumber != "" {
		t.Errorf("VABank/VANumber = %q/%q, want empty for QRIS channel", charge.VABank, charge.VANumber)
	}
}

func TestCalculateFee(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(FeeQuote{
			GrossAmount: 100000,
			FeeAmount:   3000,
			NetAmount:   97000,
			Currency:    "IDR",
		})
	})

	quote, err := c.CalculateFee(context.Background(), CalculateFeeParams{
		Amount:   100000,
		Currency: "IDR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/fees/calculate" {
		t.Errorf("got %s %s, want POST /v1/fees/calculate", gotMethod, gotPath)
	}
	if _, ok := gotBody["provider"]; ok {
		t.Errorf("expected no provider field in request body, got %v", gotBody["provider"])
	}
	if quote.FeeAmount != 3000 {
		t.Errorf("FeeAmount = %d, want 3000", quote.FeeAmount)
	}
}

func TestCalculateFeeWithChannel(t *testing.T) {
	var gotBody map[string]any
	c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeeQuote{
			GrossAmount: 100000,
			FeeAmount:   625,
			NetAmount:   99375,
			Currency:    "IDR",
		})
	})

	quote, err := c.CalculateFee(context.Background(), CalculateFeeParams{
		Amount:   100000,
		Currency: "IDR",
		Channel:  "virtual_account",
		VABank:   "BCA",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["channel"] != "virtual_account" {
		t.Errorf("body channel = %v, want virtual_account", gotBody["channel"])
	}
	if gotBody["va_bank"] != "BCA" {
		t.Errorf("body va_bank = %v, want BCA", gotBody["va_bank"])
	}
	if quote.NetAmount != 99375 {
		t.Errorf("NetAmount = %d, want 99375", quote.NetAmount)
	}
}

func TestListBanks(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"code":"BCA","name":"BCA (Bank Central Asia)","logo_url":"/bank-logos/BCA.svg","sort_order":1},
			{"code":"BRI","name":"Bank Rakyat Indonesia","logo_url":null,"sort_order":2}
		]`))
	})

	banks, err := c.ListBanks(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/banks" {
		t.Errorf("got %s %s, want GET /v1/banks", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk_test" {
		t.Errorf("Authorization = %q, want Bearer sk_test", gotAuth)
	}
	if len(banks) != 2 {
		t.Fatalf("len(banks) = %d, want 2", len(banks))
	}
	if banks[0].Code != "BCA" || banks[0].SortOrder != 1 {
		t.Errorf("banks[0] = %+v", banks[0])
	}
	if banks[0].LogoURL == nil || *banks[0].LogoURL != "/bank-logos/BCA.svg" {
		t.Errorf("banks[0].LogoURL = %v, want /bank-logos/BCA.svg", banks[0].LogoURL)
	}
	if banks[1].LogoURL != nil {
		t.Errorf("banks[1].LogoURL = %v, want nil", banks[1].LogoURL)
	}
}

func TestRegisterBankAccount(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(BankAccount{
			ID:                "ba_1",
			BankCode:          "BCA",
			AccountNumber:     "1234567890",
			AccountHolderName: "Budi Santoso",
		})
	})

	acct, err := c.RegisterBankAccount(context.Background(), RegisterBankAccountParams{
		BankCode:          "BCA",
		AccountNumber:     "1234567890",
		AccountHolderName: "Budi Santoso",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/bank-accounts" {
		t.Errorf("got %s %s, want POST /v1/bank-accounts", gotMethod, gotPath)
	}
	if gotBody["bank_code"] != "BCA" {
		t.Errorf("body bank_code = %v", gotBody["bank_code"])
	}
	if acct.ID != "ba_1" {
		t.Errorf("ID = %q, want ba_1", acct.ID)
	}
}

func TestCreatePayout(t *testing.T) {
	var gotMethod, gotPath, gotIdemKey string
	var gotBody map[string]any
	c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotIdemKey = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Payout{
			ID:            "po_1",
			BankAccountID: "ba_1",
			Mode:          "sandbox",
			Status:        "completed",
			Amount:        40000,
			Currency:      "IDR",
		})
	})

	payout, err := c.CreatePayout(context.Background(), CreatePayoutParams{
		BankAccountID: "ba_1",
		Amount:        40000,
		Currency:      "IDR",
	}, "idem-payout-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/payouts" {
		t.Errorf("got %s %s, want POST /v1/payouts", gotMethod, gotPath)
	}
	if gotIdemKey != "idem-payout-1" {
		t.Errorf("Idempotency-Key = %q", gotIdemKey)
	}
	if _, ok := gotBody["provider"]; ok {
		t.Errorf("expected no provider field in request body, got %v", gotBody["provider"])
	}
	if payout.Amount != 40000 {
		t.Errorf("Amount = %d, want 40000", payout.Amount)
	}

	// Idempotency key required.
	_, err = c.CreatePayout(context.Background(), CreatePayoutParams{
		BankAccountID: "ba_1", Amount: 1000, Currency: "IDR",
	}, "")
	if err == nil {
		t.Fatalf("expected error when idempotencyKey is empty")
	}
}

func TestGetBalance(t *testing.T) {
	tests := []struct {
		name         string
		currency     string
		wantQuery    string
		respCurrency string
	}{
		{name: "explicit currency", currency: "IDR", wantQuery: "currency=IDR", respCurrency: "IDR"},
		{name: "empty currency omits query param", currency: "", wantQuery: "", respCurrency: "IDR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotQuery string
			c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotQuery = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(Balance{Currency: tt.respCurrency, Amount: 287500})
			})

			bal, err := c.GetBalance(context.Background(), tt.currency)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMethod != http.MethodGet || gotPath != "/v1/balance" {
				t.Errorf("got %s %s, want GET /v1/balance", gotMethod, gotPath)
			}
			if gotQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", gotQuery, tt.wantQuery)
			}
			if bal.Amount != 287500 {
				t.Errorf("Amount = %d, want 287500", bal.Amount)
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	if err := c.Healthz(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/healthz" {
		t.Errorf("got %s %s, want GET /healthz", gotMethod, gotPath)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header should not be sent to /healthz, got %q", gotAuth)
	}
}

func TestAPIErrorTranslation(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errMessage string
	}{
		{name: "401 unauthorized", statusCode: http.StatusUnauthorized, errMessage: "invalid API key"},
		{name: "422 business rejection", statusCode: http.StatusUnprocessableEntity, errMessage: "insufficient available balance"},
		{name: "429 fraud velocity", statusCode: http.StatusTooManyRequests, errMessage: "velocity limit exceeded"},
		{name: "500 internal error", statusCode: http.StatusInternalServerError, errMessage: "internal error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, "sk_test", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": tt.errMessage})
			})

			_, err := c.CalculateFee(context.Background(), CalculateFeeParams{
				Amount: 1000, Currency: "IDR",
			})
			if err == nil {
				t.Fatalf("expected error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected errors.As to find *APIError, got %v (%T)", err, err)
			}
			if apiErr.StatusCode != tt.statusCode {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.statusCode)
			}
			if apiErr.Message != tt.errMessage {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.errMessage)
			}
		})
	}
}

func TestGenerateIdempotencyKey(t *testing.T) {
	a := GenerateIdempotencyKey()
	b := GenerateIdempotencyKey()
	if a == "" || b == "" {
		t.Fatalf("expected non-empty keys")
	}
	if a == b {
		t.Fatalf("expected distinct keys, got two identical: %q", a)
	}
	if len(a) != 32 { // 16 random bytes hex-encoded
		t.Errorf("len(key) = %d, want 32", len(a))
	}
}
