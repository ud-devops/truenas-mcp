// Package redact masks credential-bearing values in JSON before it reaches a
// log or an error message.
//
// It lives in its own package so that both the transport layer (which logs raw
// JSON-RPC traffic) and the TrueNAS client (which logs middleware traffic)
// share one definition of "sensitive", instead of the protocol layer having to
// import the TrueNAS client just to sanitise a log line.
package redact

import (
	"encoding/json"
	"strings"
)

// sensitiveKeyFragments match JSON object keys whose values must never be
// written to logs or error messages (e.g. bindpw in directoryservices.update).
var sensitiveKeyFragments = []string{
	"password", "passwd", "bindpw", "secret", "api_key", "apikey",
	"token", "credential", "private_key", "passphrase",
}

// IsSensitiveKey reports whether a JSON key's value should be masked.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(k, fragment) {
			return true
		}
	}
	return false
}

// Value returns a deep copy of v with values under sensitive keys masked.
func Value(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if IsSensitiveKey(k) {
				out[k] = "[REDACTED]"
			} else {
				out[k] = Value(val)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = Value(val)
		}
		return out
	default:
		return v
	}
}

// JSON returns raw with values under credential-bearing keys replaced by
// "[REDACTED]". Returns raw unchanged if it is not valid JSON.
func JSON(raw []byte) []byte {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(Value(v))
	if err != nil {
		return raw
	}
	return out
}
