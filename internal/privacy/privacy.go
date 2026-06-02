// Package privacy handles visitor IP/UA extraction and privacy-preserving
// transforms (hashing, truncation), plus basic bot detection.
package privacy

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
)

type Privacy struct {
	ipMode      string
	salt        string
	recordUA    bool
	botEnabled  bool
	botKeywords []string
}

func New(ipMode, salt string, recordUA, botEnabled bool, botKeywords []string) *Privacy {
	lk := make([]string, 0, len(botKeywords))
	for _, k := range botKeywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			lk = append(lk, k)
		}
	}
	return &Privacy{
		ipMode:      ipMode,
		salt:        salt,
		recordUA:    recordUA,
		botEnabled:  botEnabled,
		botKeywords: lk,
	}
}

// ClientIP returns the best-effort client IP.
//
// When trustProxy is true it consults realIPHeaders in order (e.g.
// CF-Connecting-IP, X-Real-IP) and returns the first non-empty value. These
// headers must be set by a trusted edge that overwrites any client-supplied
// value. As a fallback it uses the RIGHT-most X-Forwarded-For entry — the IP
// observed by the nearest trusted proxy — because the left-most XFF entry is
// client-controlled and therefore spoofable.
//
// When trustProxy is false (or no header yields a value) it falls back to the
// TCP RemoteAddr.
func ClientIP(r *http.Request, trustProxy bool, realIPHeaders []string) string {
	if trustProxy {
		for _, h := range realIPHeaders {
			if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
				return v
			}
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// StoreIP transforms a raw IP into the value to persist, per the configured
// mode. An empty string means "do not store".
func (p *Privacy) StoreIP(ip string) string {
	switch p.ipMode {
	case "none":
		return ""
	case "full":
		return ip
	case "truncate":
		return truncateIP(ip)
	default: // "hash"
		sum := sha256.Sum256([]byte(p.salt + "|" + ip))
		return hex.EncodeToString(sum[:])[:32]
	}
}

func truncateIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if v4 := parsed.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	// IPv6: keep the /48 prefix.
	return parsed.Mask(net.CIDRMask(48, 128)).String()
}

// StoreUA returns the UA string to persist (empty when UA recording is off).
func (p *Privacy) StoreUA(ua string) string {
	if !p.recordUA {
		return ""
	}
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return ua
}

// IsBot reports whether the user-agent matches a known bot keyword.
func (p *Privacy) IsBot(ua string) bool {
	if !p.botEnabled || ua == "" {
		return false
	}
	lua := strings.ToLower(ua)
	for _, k := range p.botKeywords {
		if strings.Contains(lua, k) {
			return true
		}
	}
	return false
}

// DedupKey builds the dedup identity for a visitor+page. It always uses the
// raw IP+UA and is independent of persistence privacy settings, since it is
// only kept in memory with a TTL.
func DedupKey(site, page, ip, ua string) string {
	sum := sha256.Sum256([]byte(site + "|" + page + "|" + ip + "|" + ua))
	return hex.EncodeToString(sum[:])
}
