package location

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

type Proxy struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func Digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Hash the entire trimmed URL, including authentication; display no credentials,
// path or query. Two accounts on the same endpoint can have different regions.
func IdentifyProxy(raw string) Proxy {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Proxy{}
	}
	hash := Digest(raw)
	name := "proxy"
	if u, e := url.Parse(raw); e == nil && u.Host != "" {
		name = u.Scheme + "://" + u.Host
	}
	return Proxy{ID: "proxy:" + hash, Name: name + " · " + hash[:8]}
}
