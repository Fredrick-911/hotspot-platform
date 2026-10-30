package radius

import (
	"context"
	"crypto/md5" //nolint:gosec // RFC 5176 mandates MD5 for the authenticator
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// RADIUS packet codes (RFC 5176).
const (
	CodeDisconnectRequest = 40
	CodeDisconnectACK     = 41
	CodeDisconnectNAK     = 42
)

// RADIUS attribute types we use.
const (
	attrUserName      = 1
	attrNASIPAddress  = 4
	attrAcctSessionID = 44
	attrErrorCause    = 101
)

// DefaultPort is where MikroTik listens for Disconnect/CoA ("/radius incoming").
const DefaultPort = 3799

// ErrNAK means the router answered but refused (for example, the session is
// already gone). Cause is the RADIUS Error-Cause value when it sent one.
type ErrNAK struct{ Cause uint32 }

func (e *ErrNAK) Error() string {
	if e.Cause == 503 {
		return "router has no such session (already disconnected)"
	}
	return fmt.Sprintf("router refused the disconnect (Error-Cause %d)", e.Cause)
}

// IsSessionGone reports whether err means "the session was not there", which
// the caller can treat as success.
func IsSessionGone(err error) bool {
	var nak *ErrNAK
	return errors.As(err, &nak) && nak.Cause == 503
}

// Request describes one Disconnect-Request.
type Request struct {
	Addr      string // router "ip" or "ip:port" (port defaults to 3799)
	Secret    string // the RADIUS secret shared with this router
	Username  string
	SessionID string // Acct-Session-Id from radacct
	NASIP     net.IP // optional NAS-IP-Address attribute
}

// Client sends Disconnect-Requests. The zero value works.
type Client struct {
	Timeout time.Duration // per attempt (default 3s)
	Retries int           // extra attempts after the first (default 2)
}

type attribute struct {
	typ   byte
	value []byte
}

func encodePacket(code, id byte, auth [16]byte, attrs []attribute) ([]byte, error) {
	length := 20
	for _, a := range attrs {
		if len(a.value) > 253 {
			return nil, errors.New("attribute too long")
		}
		length += 2 + len(a.value)
	}
	pkt := make([]byte, 0, length)
	pkt = append(pkt, code, id, 0, 0)
	pkt = append(pkt, auth[:]...)
	for _, a := range attrs {
		pkt = append(pkt, a.typ, byte(2+len(a.value)))
		pkt = append(pkt, a.value...)
	}
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	return pkt, nil
}

// requestAuthenticator = MD5(Code+ID+Length+16 zero bytes+Attributes+Secret).
func requestAuthenticator(pkt []byte, secret string) [16]byte {
	h := md5.New() //nolint:gosec
	h.Write(pkt[:4])
	h.Write(make([]byte, 16))
	h.Write(pkt[20:])
	h.Write([]byte(secret))
	var out [16]byte
	copy(out[:], h.Sum(nil))
	return out
}

// responseAuthenticator = MD5(Code+ID+Length+RequestAuth+Attributes+Secret).
func responseAuthenticator(resp []byte, reqAuth [16]byte, secret string) [16]byte {
	h := md5.New() //nolint:gosec
	h.Write(resp[:4])
	h.Write(reqAuth[:])
	h.Write(resp[20:])
	h.Write([]byte(secret))
	var out [16]byte
	copy(out[:], h.Sum(nil))
	return out
}

// BuildDisconnectRequest returns the wire bytes of a Disconnect-Request.
func BuildDisconnectRequest(id byte, r Request) ([]byte, error) {
	if r.Username == "" && r.SessionID == "" {
		return nil, errors.New("radius: need a username or a session id")
	}
	var attrs []attribute
	if r.Username != "" {
		attrs = append(attrs, attribute{attrUserName, []byte(r.Username)})
	}
	if r.SessionID != "" {
		attrs = append(attrs, attribute{attrAcctSessionID, []byte(r.SessionID)})
	}
	if ip4 := r.NASIP.To4(); ip4 != nil {
		attrs = append(attrs, attribute{attrNASIPAddress, ip4})
	}
	pkt, err := encodePacket(CodeDisconnectRequest, id, [16]byte{}, attrs)
	if err != nil {
		return nil, err
	}
	auth := requestAuthenticator(pkt, r.Secret)
	copy(pkt[4:20], auth[:])
	return pkt, nil
}

// ParseResponse checks a reply to the request whose authenticator was reqAuth.
// It returns nil for a Disconnect-ACK and *ErrNAK for a NAK.
func ParseResponse(resp []byte, id byte, reqAuth [16]byte, secret string) error {
	if len(resp) < 20 || int(binary.BigEndian.Uint16(resp[2:4])) != len(resp) {
		return errors.New("radius: malformed response")
	}
	if resp[1] != id {
		return errors.New("radius: response id does not match the request")
	}
	want := responseAuthenticator(resp, reqAuth, secret)
	if subtle.ConstantTimeCompare(resp[4:20], want[:]) != 1 {
		return errors.New("radius: response failed authentication (wrong shared secret?)")
	}
	switch resp[0] {
	case CodeDisconnectACK:
		return nil
	case CodeDisconnectNAK:
		nak := &ErrNAK{}
		for rest := resp[20:]; len(rest) >= 2; {
			l := int(rest[1])
			if l < 2 || l > len(rest) {
				break
			}
			if rest[0] == attrErrorCause && l == 6 {
				nak.Cause = binary.BigEndian.Uint32(rest[2:6])
			}
			rest = rest[l:]
		}
		return nak
	default:
		return fmt.Errorf("radius: unexpected response code %d", resp[0])
	}
}

// Disconnect sends the request and waits for the router's answer.
func (c Client) Disconnect(ctx context.Context, r Request) error {
	timeout, retries := c.Timeout, c.Retries
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if retries <= 0 {
		retries = 2
	}

	addr := r.Addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, fmt.Sprint(DefaultPort))
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("radius: dial %s: %w", addr, err)
	}
	defer conn.Close()

	idBuf := make([]byte, 1)
	if _, err := randRead(idBuf); err != nil {
		return err
	}
	id := idBuf[0]
	pkt, err := BuildDisconnectRequest(id, r)
	if err != nil {
		return err
	}
	var reqAuth [16]byte
	copy(reqAuth[:], pkt[4:20])

	buf := make([]byte, 4096)
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline := time.Now().Add(timeout)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		_ = conn.SetDeadline(deadline)
		if _, err := conn.Write(pkt); err != nil {
			return fmt.Errorf("radius: send: %w", err)
		}
		n, err := conn.Read(buf)
		if err != nil {
			lastErr = err
			continue // timeout: send again
		}
		return ParseResponse(buf[:n], id, reqAuth, r.Secret)
	}
	return fmt.Errorf("radius: no answer from %s after %d attempts: %w", addr, retries+1, lastErr)
}
