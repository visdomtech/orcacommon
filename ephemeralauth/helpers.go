package ephemeralauth

import "encoding/base64"

// encodeBase64URL encodes data to base64url without padding.
func encodeBase64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// decodeBase64URL decodes base64url without padding.
func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
