// Conformance tests for LoadFromKMS on the treasury-side adapter.
// Mirror of the broker-side suite; documents that the treasury
// surface fetches only the two required credentials (clientID +
// developerAPIKey), with no webhook keys.
//
// Source-of-design: Public-Spec
// Source-ref: legal/partnerships/northcapital/
// NorthCapital_Lux_Integration_Brief.md §2.

package northcapital

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// mockKMS is the in-memory KMSGetter used by every test in this file.
type mockKMS struct {
	mu       sync.Mutex
	values   map[string]string
	errs     map[string]error
	called   []string
	notFound error
}

func newMockKMS() *mockKMS {
	return &mockKMS{
		values:   make(map[string]string),
		errs:     make(map[string]error),
		notFound: errors.New("zapclient: secret not found"),
	}
}

func (m *mockKMS) Get(_ context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called = append(m.called, name)
	if err, ok := m.errs[name]; ok {
		return "", err
	}
	if v, ok := m.values[name]; ok {
		return v, nil
	}
	return "", m.notFound
}

func (m *mockKMS) calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.called))
	copy(out, m.called)
	return out
}

// --- happy paths ---

func TestLoadFromKMS_SandboxSuccess(t *testing.T) {
	kms := newMockKMS()
	kms.values["shared/northcapital/sandbox/clientID"] = "cid-sandbox"
	kms.values["shared/northcapital/sandbox/developerAPIKey"] = "dak-sandbox"

	var cfg Config
	if err := cfg.LoadFromKMS(context.Background(), kms, "sandbox"); err != nil {
		t.Fatalf("LoadFromKMS: %v", err)
	}
	if cfg.ClientID != "cid-sandbox" {
		t.Fatalf("ClientID: got %q", cfg.ClientID)
	}
	if cfg.DeveloperAPIKey != "dak-sandbox" {
		t.Fatalf("DeveloperAPIKey: got %q", cfg.DeveloperAPIKey)
	}

	// Path shape: treasury fetches ONLY clientID + developerAPIKey.
	got := kms.calls()
	sort.Strings(got)
	want := []string{
		"shared/northcapital/sandbox/clientID",
		"shared/northcapital/sandbox/developerAPIKey",
	}
	sort.Strings(want)
	if !equalStrSlice(got, want) {
		t.Fatalf("KMS path shape: got %v, want %v", got, want)
	}
}

func TestLoadFromKMS_ProdSuccess(t *testing.T) {
	kms := newMockKMS()
	kms.values["shared/northcapital/prod/clientID"] = "cid-prod"
	kms.values["shared/northcapital/prod/developerAPIKey"] = "dak-prod"

	var cfg Config
	if err := cfg.LoadFromKMS(context.Background(), kms, "prod"); err != nil {
		t.Fatalf("LoadFromKMS: %v", err)
	}
	if cfg.ClientID != "cid-prod" || cfg.DeveloperAPIKey != "dak-prod" {
		t.Fatalf("required fields: %+v", cfg)
	}
}

// --- missing required keys ---

func TestLoadFromKMS_MissingClientID(t *testing.T) {
	kms := newMockKMS()
	kms.values["shared/northcapital/sandbox/developerAPIKey"] = "dak"

	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "sandbox")
	if !errors.Is(err, ErrKMSKeyMissing) {
		t.Fatalf("got %v, want errors.Is ErrKMSKeyMissing", err)
	}
}

func TestLoadFromKMS_MissingDeveloperAPIKey(t *testing.T) {
	kms := newMockKMS()
	kms.values["shared/northcapital/sandbox/clientID"] = "cid"

	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "sandbox")
	if !errors.Is(err, ErrKMSKeyMissing) {
		t.Fatalf("got %v, want errors.Is ErrKMSKeyMissing", err)
	}
}

func TestLoadFromKMS_EmptyValueTreatedAsMissing(t *testing.T) {
	kms := newMockKMS()
	kms.values["shared/northcapital/sandbox/clientID"] = ""
	kms.values["shared/northcapital/sandbox/developerAPIKey"] = "dak"

	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "sandbox")
	if !errors.Is(err, ErrKMSKeyMissing) {
		t.Fatalf("empty clientID: got %v, want ErrKMSKeyMissing", err)
	}
}

// --- transport / decryption errors ---

func TestLoadFromKMS_DecryptionError(t *testing.T) {
	kms := newMockKMS()
	kms.errs["shared/northcapital/sandbox/clientID"] = fmt.Errorf("aead: decrypt: bad tag")

	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "sandbox")
	if !errors.Is(err, ErrKMSDecryption) {
		t.Fatalf("got %v, want errors.Is ErrKMSDecryption", err)
	}
}

func TestLoadFromKMS_TransportError(t *testing.T) {
	kms := newMockKMS()
	kms.values["shared/northcapital/sandbox/clientID"] = "cid"
	kms.errs["shared/northcapital/sandbox/developerAPIKey"] = fmt.Errorf("dial tcp: i/o timeout")

	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "sandbox")
	if !errors.Is(err, ErrKMSDecryption) {
		t.Fatalf("got %v, want errors.Is ErrKMSDecryption", err)
	}
}

// --- argument validation ---

func TestLoadFromKMS_NilGetter(t *testing.T) {
	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), nil, "sandbox")
	if err == nil {
		t.Fatal("nil KMSGetter: expected error, got nil")
	}
}

func TestLoadFromKMS_EmptyEnv(t *testing.T) {
	kms := newMockKMS()
	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "")
	if err == nil {
		t.Fatal("empty env: expected error, got nil")
	}
}

func TestLoadFromKMS_UnknownEnv(t *testing.T) {
	kms := newMockKMS()
	var cfg Config
	err := cfg.LoadFromKMS(context.Background(), kms, "staging")
	if err == nil {
		t.Fatal("unknown env: expected error, got nil")
	}
}

// --- helpers ---

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKMSPath(t *testing.T) {
	cases := []struct {
		env, field, want string
	}{
		{"sandbox", "clientID", "shared/northcapital/sandbox/clientID"},
		{"prod", "developerAPIKey", "shared/northcapital/prod/developerAPIKey"},
	}
	for _, tc := range cases {
		if got := kmsPath(tc.env, tc.field); got != tc.want {
			t.Fatalf("kmsPath(%q,%q)=%q want %q", tc.env, tc.field, got, tc.want)
		}
	}
}
