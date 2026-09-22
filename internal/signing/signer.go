package signing

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
)

// chunk60 splits s into 60-character pieces (the X-Ops-Authorization-N width).
func chunk60(s string) []string {
	out := make([]string, 0, (len(s)+59)/60)
	for len(s) > 60 {
		out = append(out, s[:60])
		s = s[60:]
	}
	if len(s) > 0 {
		out = append(out, s)
	}
	return out
}

// SignHeaders signs r with key and returns the headers to add to the request.
func SignHeaders(r Request, key *rsa.PrivateKey) (http.Header, error) {
	// Hash the body once and reuse it for both the string-to-sign and the
	// X-Ops-Content-Hash header rather than hashing it twice.
	contentHash := ContentHash(r.Body)
	digest := sha256.Sum256([]byte(canonicalRequest(r, contentHash)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	h := http.Header{}
	h.Set("X-Ops-Sign", "version=1.3")
	h.Set("X-Ops-UserId", r.UserID)
	h.Set("X-Ops-Timestamp", r.Timestamp)
	h.Set("X-Ops-Content-Hash", contentHash)
	h.Set("X-Ops-Server-API-Version", r.apiVersion())
	for i, chunk := range chunk60(base64.StdEncoding.EncodeToString(sig)) {
		h.Set("X-Ops-Authorization-"+strconv.Itoa(i+1), chunk)
	}
	return h, nil
}
