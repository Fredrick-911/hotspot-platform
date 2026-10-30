package session

import (
	"regexp"
	"testing"
)

func TestGeneratePIN(t *testing.T) {
	re := regexp.MustCompile(`^[1-9][0-9]{3}$`)
	for i := 0; i < 500; i++ {
		if p := GeneratePIN(); !re.MatchString(p) {
			t.Fatalf("PIN %q is not 4 digits", p)
		}
	}
}

func TestNewUsernameIsUniqueAndSafe(t *testing.T) {
	// The username goes into FreeRADIUS SQL queries and a MikroTik login form:
	// only a-z, 0-9 and '-' are allowed.
	re := regexp.MustCompile(`^0712345678-[a-z0-9]{5}$`)
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		u := NewUsername("0712345678")
		if !re.MatchString(u) {
			t.Fatalf("bad username %q", u)
		}
		if seen[u] {
			t.Fatalf("duplicate username %q after %d tries", u, i)
		}
		seen[u] = true
	}
}
