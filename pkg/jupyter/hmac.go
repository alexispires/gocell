package jupyter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignHMAC computes the HMAC-SHA256 signature for the 4 JSON parts of a Jupyter message.
func SignHMAC(key []byte, header, parentHeader, metadata, content []byte) string {
	if len(key) == 0 {
		return ""
	}
	h := hmac.New(sha256.New, key)
	h.Write(header)
	h.Write(parentHeader)
	h.Write(metadata)
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// ValidateHMAC checks whether the provided signature matches the HMAC computed over the message parts.
func ValidateHMAC(key []byte, signature string, header, parentHeader, metadata, content []byte) bool {
	if len(key) == 0 {
		return true
	}
	expected := SignHMAC(key, header, parentHeader, metadata, content)
	return hmac.Equal([]byte(signature), []byte(expected))
}
