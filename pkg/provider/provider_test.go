package provider

import "testing"

func TestDeriveIdempotencyKey_Stable(t *testing.T) {
	a := DeriveIdempotencyKey("org-1", "/v1/transfers/ach", []byte(`{"amount":"100","currency":"USD"}`))
	b := DeriveIdempotencyKey("org-1", "/v1/transfers/ach", []byte(`{"amount":"100","currency":"USD"}`))
	if a != b {
		t.Fatalf("expected stable digest, got %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %d", len(a))
	}
}

func TestDeriveIdempotencyKey_DifferentInputsDiffer(t *testing.T) {
	base := DeriveIdempotencyKey("org-1", "/v1/transfers/ach", []byte(`{"amount":"100"}`))
	cases := map[string]string{
		"orgID":    DeriveIdempotencyKey("org-2", "/v1/transfers/ach", []byte(`{"amount":"100"}`)),
		"endpoint": DeriveIdempotencyKey("org-1", "/v1/transfers/wire", []byte(`{"amount":"100"}`)),
		"body":     DeriveIdempotencyKey("org-1", "/v1/transfers/ach", []byte(`{"amount":"200"}`)),
	}
	for field, v := range cases {
		if v == base {
			t.Errorf("digest must differ when %s differs", field)
		}
	}
}

// Boundary-injection test: orgID="a" + endpoint="bc" must NOT collide with
// orgID="ab" + endpoint="c". The 0x00 separator guarantees that.
func TestDeriveIdempotencyKey_NoBoundaryCollision(t *testing.T) {
	x := DeriveIdempotencyKey("a", "bc", []byte("body"))
	y := DeriveIdempotencyKey("ab", "c", []byte("body"))
	if x == y {
		t.Fatalf("boundary collision: %q == %q", x, y)
	}
}
