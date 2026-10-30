package radius

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

const testSecret = "s3cret-s3cret"

// fakeRouter answers Disconnect-Requests the way a MikroTik does: it checks the
// request authenticator and replies ACK or NAK signed with the shared secret.
func fakeRouter(t *testing.T, secret string, reply func(userName string) (code byte, cause uint32)) (addr string, seen chan map[byte][]byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	seen = make(chan map[byte][]byte, 8)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pkt := append([]byte(nil), buf[:n]...)
			var reqAuth [16]byte
			copy(reqAuth[:], pkt[4:20])
			if want := requestAuthenticator(pkt, secret); want != reqAuth {
				continue // wrong secret: a real router stays silent
			}
			attrs := map[byte][]byte{}
			for rest := pkt[20:]; len(rest) >= 2; rest = rest[rest[1]:] {
				attrs[rest[0]] = append([]byte(nil), rest[2:rest[1]]...)
			}
			seen <- attrs
			code, cause := reply(string(attrs[attrUserName]))
			var extra []attribute
			if cause != 0 {
				v := make([]byte, 4)
				binary.BigEndian.PutUint32(v, cause)
				extra = append(extra, attribute{attrErrorCause, v})
			}
			resp, _ := encodePacket(code, pkt[1], [16]byte{}, extra)
			ra := responseAuthenticator(resp, reqAuth, secret)
			copy(resp[4:20], ra[:])
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String(), seen
}

func TestDisconnectACK(t *testing.T) {
	addr, seen := fakeRouter(t, testSecret, func(string) (byte, uint32) { return CodeDisconnectACK, 0 })
	err := Client{Timeout: time.Second}.Disconnect(context.Background(), Request{
		Addr: addr, Secret: testSecret, Username: "0712345678-ab12c", SessionID: "8100001f", NASIP: net.ParseIP("10.9.0.2"),
	})
	if err != nil {
		t.Fatalf("Disconnect = %v, want nil", err)
	}
	attrs := <-seen
	if string(attrs[attrUserName]) != "0712345678-ab12c" || string(attrs[attrAcctSessionID]) != "8100001f" {
		t.Fatalf("router saw attributes %v", attrs)
	}
	if got := net.IP(attrs[attrNASIPAddress]).String(); got != "10.9.0.2" {
		t.Fatalf("NAS-IP-Address = %s", got)
	}
}

func TestDisconnectNAKSessionGone(t *testing.T) {
	addr, _ := fakeRouter(t, testSecret, func(string) (byte, uint32) { return CodeDisconnectNAK, 503 })
	err := Client{Timeout: time.Second}.Disconnect(context.Background(), Request{Addr: addr, Secret: testSecret, Username: "x"})
	if !IsSessionGone(err) {
		t.Fatalf("err = %v, want session-gone NAK", err)
	}
	var nak *ErrNAK
	if !errors.As(err, &nak) || nak.Cause != 503 {
		t.Fatalf("err = %#v", err)
	}
}

func TestDisconnectWrongSecret(t *testing.T) {
	// The router only answers requests signed with ITS secret, so we get no reply.
	addr, _ := fakeRouter(t, testSecret, func(string) (byte, uint32) { return CodeDisconnectACK, 0 })
	err := Client{Timeout: 80 * time.Millisecond, Retries: 1}.Disconnect(context.Background(), Request{Addr: addr, Secret: "wrong", Username: "x"})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestResponseMustBeSignedWithTheSecret(t *testing.T) {
	var reqAuth [16]byte
	resp, _ := encodePacket(CodeDisconnectACK, 7, [16]byte{}, nil)
	ra := responseAuthenticator(resp, reqAuth, "attacker-guess")
	copy(resp[4:20], ra[:])
	if err := ParseResponse(resp, 7, reqAuth, testSecret); err == nil {
		t.Fatal("a forged ACK was accepted")
	}
}

func TestBuildRequiresSomethingToIdentify(t *testing.T) {
	if _, err := BuildDisconnectRequest(1, Request{Secret: testSecret}); err == nil {
		t.Fatal("expected an error with no username and no session id")
	}
}
