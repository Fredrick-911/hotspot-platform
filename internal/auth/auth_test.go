package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if ok, err := CheckPassword(hash, "correct horse battery"); err != nil || !ok {
		t.Fatalf("right password rejected: ok=%v err=%v", ok, err)
	}
	if ok, _ := CheckPassword(hash, "correct horse batterY"); ok {
		t.Fatal("wrong password accepted")
	}
	// Two hashes of the same password differ (random salt).
	other, _ := HashPassword("correct horse battery")
	if hash == other {
		t.Fatal("salt is not random")
	}
}

func TestPasswordRules(t *testing.T) {
	if _, err := HashPassword("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("err = %v, want ErrPasswordTooShort", err)
	}
	for _, bad := range []string{"", "x$y", "pbkdf2-sha256$abc$AA$AA", "md5$1$AA$AA"} {
		if _, err := CheckPassword(bad, "whatever"); !errors.Is(err, ErrBadHash) {
			t.Errorf("CheckPassword(%q) err = %v, want ErrBadHash", bad, err)
		}
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 19 || p[4] != '-' || p[9] != '-' || p[14] != '-' {
			t.Fatalf("unexpected shape %q", p)
		}
		if len(p) < MinPasswordLen {
			t.Fatalf("generated password shorter than the minimum: %q", p)
		}
		if seen[p] {
			t.Fatalf("duplicate password %q", p)
		}
		seen[p] = true
	}
}

func TestNormalizeEmail(t *testing.T) {
	good := map[string]string{"  Ivy@Example.COM ": "ivy@example.com", "a.b+c@d.co.ke": "a.b+c@d.co.ke"}
	for in, want := range good {
		if got, err := NormalizeEmail(in); err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "nope", "a@b", "@x.com", "a b@x.com", "a@x.com,b@x.com", "a@"} {
		if _, err := NormalizeEmail(in); !errors.Is(err, ErrBadEmail) {
			t.Errorf("NormalizeEmail(%q) err = %v, want ErrBadEmail", in, err)
		}
	}
}

func TestSigner(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	tok, err := s.Sign(Claims{Kind: "staff", Subject: "user-1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok)
	if err != nil || c.Subject != "user-1" || c.Kind != "staff" {
		t.Fatalf("Verify = %+v, %v", c, err)
	}

	// Tampering with either half is rejected.
	payload, sig, _ := strings.Cut(tok, ".")
	for _, bad := range []string{payload + "x." + sig, payload + "." + sig + "x", "." + sig, payload + ".", "garbage", ""} {
		if _, err := s.Verify(bad); !errors.Is(err, ErrTokenInvalid) {
			t.Errorf("Verify(%q) err = %v, want ErrTokenInvalid", bad, err)
		}
	}

	// A token signed with another secret is rejected.
	other := NewSigner([]byte("another-secret-another-secret-12"))
	other.now = s.now
	foreign, _ := other.Sign(Claims{Kind: "staff", Subject: "user-1"}, time.Hour)
	if _, err := s.Verify(foreign); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("foreign token err = %v, want ErrTokenInvalid", err)
	}

	// Expiry.
	s.now = func() time.Time { return now.Add(time.Hour) }
	if _, err := s.Verify(tok); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expired token err = %v, want ErrTokenExpired", err)
	}
}

func TestCallbackToken(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	tok := s.Token("payhero", "WIFI-ABC123")
	if len(tok) != 32 {
		t.Fatalf("token length %d, want 32", len(tok))
	}
	if !s.CheckToken("payhero", "WIFI-ABC123", tok) {
		t.Fatal("valid token rejected")
	}
	if s.CheckToken("payhero", "WIFI-ABC124", tok) || s.CheckToken("other", "WIFI-ABC123", tok) || s.CheckToken("payhero", "WIFI-ABC123", "") {
		t.Fatal("token accepted for the wrong reference/label/empty")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("ip|a@b.c"); !ok {
			t.Fatalf("blocked after only %d failures", i)
		}
		l.Fail("ip|a@b.c")
	}
	ok, wait := l.Allowed("ip|a@b.c")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("Allowed = %v, %v; want blocked with a wait under a minute", ok, wait)
	}
	if ok, _ := l.Allowed("other-key"); !ok {
		t.Fatal("one key's failures must not block another")
	}

	now = now.Add(61 * time.Second)
	if ok, _ := l.Allowed("ip|a@b.c"); !ok {
		t.Fatal("still blocked after the window passed")
	}

	l.Fail("k")
	l.Fail("k")
	l.Fail("k")
	l.Reset("k")
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("Reset did not clear the failures")
	}
}
