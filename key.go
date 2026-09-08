package cinc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// GenerateKeyPair generates a 2048-bit RSA key pair and returns it PEM-encoded:
// the private key as PKCS#1 ("RSA PRIVATE KEY") and the public key as PKIX
// ("PUBLIC KEY"). It is the generation counterpart to ParseKey/LoadKeyFile,
// used to mint a new client identity — for example, registering a node's
// client by handing the server the public key while keeping the private key.
func GenerateKeyPair() (privatePEM, publicPEM string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("cinc: generate key: %w", err)
	}
	privateBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	publicBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("cinc: marshal public key: %w", err)
	}
	publicPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicBytes,
	})
	return string(privateBytes), string(publicPEMBytes), nil
}

// ParseKey parses a PEM-encoded RSA private key (PKCS#1 or PKCS#8).
func ParseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("cinc: no PEM block found in key data")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cinc: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("cinc: key is %T, want *rsa.PrivateKey", parsed)
	}
	return key, nil
}

// LoadKeyFile reads and parses an RSA private key from a file path.
func LoadKeyFile(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cinc: read key file: %w", err)
	}
	return ParseKey(data)
}
