// internal/device/wireguard.go
package device

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateWireGuardKeypair creates a base64-encoded Curve25519 private/public keypair
// compatible with WireGuard and MikroTik RouterOS v7.
func GenerateWireGuardKeypair() (privateKey, publicKey string, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate x25519 key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv.Bytes()),
		base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()),
		nil
}
