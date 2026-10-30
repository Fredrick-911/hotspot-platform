package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Claims is what a signed cookie carries.
type Claims struct {
	Kind      string `json:"k"`           // "staff" or "customer"
	Subject   string `json:"s"`           // user id (staff) or session id (customer)
	TenantID  string `json:"t,omitempty"` // customers: the tenant the pass belongs to
	ExpiresAt int64  `json:"e"`           // unix seconds
}

var (
	ErrTokenInvalid = errors.New("invalid token")
	ErrTokenExpired = errors.New("token expired")
)

// Signer makes and checks tamper-proof cookie values:
// base64url(json claims) + "." + base64url(HMAC-SHA256).
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner returns a Signer using secret. The secret should be 32+ random bytes.
func NewSigner(secret []byte) *Signer { return &Signer{key: secret, now: time.Now} }

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// Sign returns a token for c that is valid for ttl.
func (s *Signer) Sign(c Claims, ttl time.Duration) (string, error) {
	c.ExpiresAt = s.now().Add(ttl).Unix()
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload)), nil
}

// Verify checks the signature and expiry and returns the claims.
func (s *Signer) Verify(token string) (Claims, error) {
	var c Claims
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return c, ErrTokenInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(payload)) {
		return c, ErrTokenInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return c, ErrTokenInvalid
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Claims{}, ErrTokenInvalid
	}
	if s.now().Unix() >= c.ExpiresAt {
		return Claims{}, ErrTokenExpired
	}
	return c, nil
}

// Token returns an HMAC of label+value as hex. Used for the PayHero callback
// URL: only we can produce the token for a given payment reference.
func (s *Signer) Token(label, value string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(label))
	m.Write([]byte{0})
	m.Write([]byte(value))
	sum := m.Sum(nil)
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 32)
	for _, b := range sum[:16] {
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(out)
}

// CheckToken reports whether got is the valid Token for label+value.
func (s *Signer) CheckToken(label, value, got string) bool {
	return hmac.Equal([]byte(s.Token(label, value)), []byte(got))
}
