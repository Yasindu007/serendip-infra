package api

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/audit"
)

// Server limits (specs/016-ui-mvp plan.md decision 9).
const (
	MaxBodyBytes      = 1 << 20
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 60 * time.Second
	IdleTimeout       = 120 * time.Second
	MaxHeaderBytes    = 64 << 10
)

// contentSecurityPolicy is the console's CSP (plan.md decision 8): scripts,
// styles, fonts and connections from this origin only, no inline script,
// no eval, no framing, no plugins.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
	"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

// setSecurityHeaders sets the headers every browser-facing response carries.
func setSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
}

// apiHardening wraps the /v1 subtree: no caching of any response, and
// request bodies capped at MaxBodyBytes.
func apiHardening(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.ContentLength > MaxBodyBytes {
			writeTooLarge(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func writeTooLarge(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, http.StatusRequestEntityTooLarge, "request body too large",
		"request bodies are limited to 1 MiB", audit.RequestIDFrom(r.Context()))
}

// decodeBody decodes r's JSON body into v. On failure it writes the
// response (413 for an oversized body, 400 otherwise) and returns false.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeTooLarge(w, r)
		return false
	}
	WriteProblem(w, http.StatusBadRequest, "malformed request body", err.Error(), audit.RequestIDFrom(r.Context()))
	return false
}

// decodeOptionalBody is decodeBody for a body that may be absent: an empty
// body, sized or chunked, leaves v as it is.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeTooLarge(w, r)
		return false
	}
	WriteProblem(w, http.StatusBadRequest, "malformed request body", err.Error(), audit.RequestIDFrom(r.Context()))
	return false
}

// clientIP is the TCP peer's address. Forwarding headers are ignored on
// purpose: nothing sits in front of serendipd, so any such header is
// attacker-supplied.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// One IPv6 client usually holds a whole /64; limit it as one.
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return host
}

// rateLimiter is a token bucket per client address: perMinute tokens a
// minute, up to burst. The table is capped; when full, idle entries go
// first, then an arbitrary one, so a flood of addresses can't grow it.
type rateLimiter struct {
	perMinute, burst float64
	now              func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

const maxRateBuckets = 4096

func newRateLimiter(perMinute, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{perMinute: float64(perMinute), burst: float64(burst), now: now, buckets: map[string]*bucket{}}
}

// allow takes a token for key, or reports how long until one is available.
func (l *rateLimiter) allow(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxRateBuckets {
			l.evictLocked(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Minutes()*l.perMinute)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	return time.Duration((1 - b.tokens) / l.perMinute * float64(time.Minute)), false
}

func (l *rateLimiter) evictLocked(now time.Time) {
	refill := time.Duration(l.burst / l.perMinute * float64(time.Minute))
	for k, b := range l.buckets {
		if now.Sub(b.last) >= refill {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets {
		if len(l.buckets) < maxRateBuckets {
			break
		}
		delete(l.buckets, k)
	}
}
