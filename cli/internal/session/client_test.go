package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	waffle "github.com/very-good-labs/waffle-go"
)

func TestLogin(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "sess_abc"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	token, err := c.Login(context.Background(), "a@b.co", "hunter22")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token != "sess_abc" {
		t.Errorf("token = %q, want sess_abc", token)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/merchants/login" {
		t.Errorf("got %s %s, want POST /v1/merchants/login", gotMethod, gotPath)
	}
	if gotAuth != "" {
		t.Errorf("login must not send a credential, got Authorization %q", gotAuth)
	}
	if gotBody["email"] != "a@b.co" || gotBody["password"] != "hunter22" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestLoginWrongCredentialsIsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid email or password"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Login(context.Background(), "a@b.co", "wrong")
	var apiErr *waffle.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *waffle.APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != "invalid email or password" {
		t.Errorf("APIError = %d %q", apiErr.StatusCode, apiErr.Message)
	}
}

func TestSessionTokenAttachedOnMeRoutes(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/merchants/me/profile":
			_ = json.NewEncoder(w).Encode(Profile{MerchantID: "m1", Email: "a@b.co", Status: "active"})
		case "/v1/merchants/me/api-keys":
			_ = json.NewEncoder(w).Encode([]APIKeySummary{})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "sess_token")
	if _, err := c.GetProfile(context.Background()); err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if gotAuth != "Bearer sess_token" || gotPath != "/v1/merchants/me/profile" {
		t.Errorf("profile call: Authorization=%q path=%q", gotAuth, gotPath)
	}
	if _, err := c.ListAPIKeys(context.Background()); err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if gotAuth != "Bearer sess_token" {
		t.Errorf("api-keys call Authorization = %q", gotAuth)
	}
}

func TestCreateAPIKeyDecodesPlaintext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "laptop" {
			t.Errorf("body name = %q", body["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(IssuedKey{Mode: "sandbox", APIKey: "sk_sandbox_new"})
	}))
	defer srv.Close()

	key, err := New(srv.URL, "sess").CreateAPIKey(context.Background(), "laptop")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if key.Mode != "sandbox" || key.APIKey != "sk_sandbox_new" {
		t.Errorf("key = %+v", key)
	}
}

func TestRevokeAPIKey(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL, "sess").RevokeAPIKey(context.Background(), "key1"); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/merchants/me/api-keys/key1/revoke" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
}
