// KMS hydration for the treasury-side TransactAPI adapter. Same
// design as the broker-side adapter (broker/pkg/provider/northcapital/
// kms.go): a small KMSGetter interface the canonical luxfi/kms
// client satisfies via a one-line wrapper at treasuryd startup,
// returning plaintext values per KMS path. The per-tenant-KEK unwrap
// is server-side in the KMS process.
//
// treasuryd wiring sketch:
//
//	import "github.com/luxfi/kms"
//
//	type kmsAdapter struct{ cfg kms.Config }
//	func (a *kmsAdapter) Get(ctx context.Context, name string) (string, error) {
//	    return kms.GetWith(ctx, a.cfg, name)
//	}
//
//	cfg := northcapital.Config{BaseURL: northcapital.SandboxURL}
//	if err := cfg.LoadFromKMS(ctx, &kmsAdapter{cfg: kms.Config{Env: env}}, "sandbox"); err != nil {
//	    log.Fatalf("northcapital LoadFromKMS: %v", err)
//	}
//	p := northcapital.New(cfg)
//
// The treasury-side Config holds only ClientID + DeveloperAPIKey —
// no webhook keys, because the inbound webhook surface is owned by
// the broker-side adapter. The KMS path scheme is identical
// (shared/northcapital/<env>/<field>); this file only fetches the
// two fields the treasury adapter needs.
//
// Source-of-design: Public-Spec
// Source-ref: legal/partnerships/northcapital/
// NorthCapital_Lux_Integration_Brief.md §2; github.com/luxfi/kms
// public Go surface (kms.Get / kms.GetWith).
//
// Authored under the Independent-Implementation Clean-Room Engineering
// Protocol (legal/INDEPENDENT-IMPLEMENTATION-CLEAN-ROOM-PROTOCOL.md).

package northcapital

import (
	"context"
	"errors"
	"fmt"
)

// KMSGetter is the minimal surface LoadFromKMS depends on. Returns
// the plaintext value of name; the per-tenant-KEK unwrap is
// server-side. Mirrors the broker-side interface so a single adapter
// instance can serve both packages.
type KMSGetter interface {
	Get(ctx context.Context, name string) (string, error)
}

// ErrKMSKeyMissing is the sentinel LoadFromKMS returns when one of
// the required KMS paths is absent.
var ErrKMSKeyMissing = errors.New("northcapital: required KMS key missing")

// ErrKMSDecryption is the sentinel LoadFromKMS returns when KMS
// retrieval failed for a transport or decryption reason.
var ErrKMSDecryption = errors.New("northcapital: KMS decryption / transport error")

// kmsPath returns the canonical KMS key name for (env, field) per
// the integration brief §2: shared/northcapital/<env>/<field>.
func kmsPath(env, field string) string {
	return "shared/northcapital/" + env + "/" + field
}

// loadOne fetches one KMS path with the documented error mapping.
// "not found" → ErrKMSKeyMissing; anything else → ErrKMSDecryption.
func loadOne(ctx context.Context, kms KMSGetter, env, field string) (string, error) {
	path := kmsPath(env, field)
	v, err := kms.Get(ctx, path)
	if err != nil {
		if isMissingErr(err) {
			return "", fmt.Errorf("%w: %s: %w", ErrKMSKeyMissing, path, err)
		}
		return "", fmt.Errorf("%w: %s: %w", ErrKMSDecryption, path, err)
	}
	if v == "" {
		return "", fmt.Errorf("%w: %s (empty value)", ErrKMSKeyMissing, path)
	}
	return v, nil
}

// isMissingErr classifies an error as "not found". luxfi/kms's
// zapclient.ErrNotFound is the canonical sentinel; we fall back to
// substring detection to support KMSGetter wrappers that lose the
// typed error.
func isMissingErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "not found") || contains(msg, "NotFound")
}

// contains is a strings.Contains alias that avoids the package
// import here.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// LoadFromKMS hydrates cfg from the documented KMS paths under
//
//	shared/northcapital/<env>/clientID
//	shared/northcapital/<env>/developerAPIKey
//
// env is "sandbox" or "prod". Both keys are required for the
// treasury-side adapter (no optional fields — the payment path uses
// only the v3 credential pair). Missing → ErrKMSKeyMissing; KMS
// transport / decryption failure → ErrKMSDecryption. BaseURL is
// caller-supplied so the caller decides between SandboxURL and
// ProdURL.
func (cfg *Config) LoadFromKMS(ctx context.Context, kms KMSGetter, env string) error {
	if kms == nil {
		return errors.New("northcapital: LoadFromKMS requires a non-nil KMSGetter")
	}
	if env == "" {
		return errors.New("northcapital: LoadFromKMS requires env (\"sandbox\" or \"prod\")")
	}
	if env != "sandbox" && env != "prod" {
		return fmt.Errorf("northcapital: LoadFromKMS env %q not recognized (want \"sandbox\" or \"prod\")", env)
	}

	clientID, err := loadOne(ctx, kms, env, "clientID")
	if err != nil {
		return err
	}
	developerAPIKey, err := loadOne(ctx, kms, env, "developerAPIKey")
	if err != nil {
		return err
	}

	cfg.ClientID = clientID
	cfg.DeveloperAPIKey = developerAPIKey
	return nil
}
