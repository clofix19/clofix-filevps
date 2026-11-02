package server

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authLoginFailLimit    = 10
	authLoginFailWindow   = 10 * time.Minute
	authEmailFailLimit    = 5
	authEmailFailWindow   = 15 * time.Minute
	authRegisterLimit     = 5
	authRegisterWindow    = 30 * time.Minute
	authMFAFailLimit      = 10
	authMFAFailWindow     = 10 * time.Minute
	authPageGetLimit      = 120
	authPageGetWindow     = time.Minute
	authLoginBurstLimit   = 30
	authLoginBurstWindow  = time.Minute
)

func setAuthSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
}

func authJSONContentType(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	return strings.HasPrefix(ct, "application/json")
}

func sameOriginAuth(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Server) authHitsLimited(bucket string, limit int, window time.Duration) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	cut := time.Now().Add(-window)
	keep := s.failures[bucket][:0]
	for _, t := range s.failures[bucket] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if keep == nil {
		keep = []time.Time{}
	}
	s.failures[bucket] = keep
	return len(keep) >= limit
}

func (s *Server) noteAuthHit(bucket string) {
	s.failMu.Lock()
	s.failures[bucket] = append(s.failures[bucket], time.Now())
	s.failMu.Unlock()
}

func (s *Server) clearAuthHits(bucket string) {
	s.failMu.Lock()
	delete(s.failures, bucket)
	s.failMu.Unlock()
}

func authIPBuckets(ip, kind string) (burst, fail, email string) {
	burst = "auth:" + kind + ":burst:" + ip
	fail = "auth:" + kind + ":fail:" + ip
	return burst, fail, ""
}

func authEmailBucket(email string) string {
	return "auth:login:email:" + strings.ToLower(strings.TrimSpace(email))
}

func (s *Server) rejectAuthAbuse(w http.ResponseWriter, r *http.Request, kind string) bool {
	setAuthSecurityHeaders(w)
	if !sameOriginAuth(r) {
		fail(w, http.StatusForbidden, "origin not allowed")
		return true
	}
	if !authJSONContentType(r) {
		fail(w, http.StatusUnsupportedMediaType, "application/json required")
		return true
	}
	ip := clientIP(r)
	if ip == "" {
		ip = "unknown"
	}
	burst, failBucket, _ := authIPBuckets(ip, kind)
	var limit int
	var window time.Duration
	switch kind {
	case "register":
		limit, window = authRegisterLimit, authRegisterWindow
		if s.authHitsLimited(burst, limit, window) {
			w.Header().Set("Retry-After", "1800")
			fail(w, http.StatusTooManyRequests, "too many registration attempts; try again later")
			return true
		}
		s.noteAuthHit(burst)
		return false
	case "mfa":
		if s.authHitsLimited(failBucket, authMFAFailLimit, authMFAFailWindow) {
			w.Header().Set("Retry-After", "600")
			fail(w, http.StatusTooManyRequests, "too many MFA attempts; try again later")
			return true
		}
		if s.authHitsLimited(burst, authLoginBurstLimit, authLoginBurstWindow) {
			w.Header().Set("Retry-After", "60")
			fail(w, http.StatusTooManyRequests, "too many requests; slow down")
			return true
		}
		s.noteAuthHit(burst)
		return false
	default: // login
		if s.authHitsLimited(failBucket, authLoginFailLimit, authLoginFailWindow) {
			w.Header().Set("Retry-After", "600")
			fail(w, http.StatusTooManyRequests, "too many failed sign-in attempts; try again later")
			return true
		}
		if s.authHitsLimited(burst, authLoginBurstLimit, authLoginBurstWindow) {
			w.Header().Set("Retry-After", "60")
			fail(w, http.StatusTooManyRequests, "too many requests; slow down")
			return true
		}
		s.noteAuthHit(burst)
		return false
	}
}

func (s *Server) noteLoginFailure(ip, email string) {
	if ip == "" {
		ip = "unknown"
	}
	_, failBucket, _ := authIPBuckets(ip, "login")
	s.noteAuthHit(failBucket)
	if email != "" {
		s.noteAuthHit(authEmailBucket(email))
	}
}

func (s *Server) noteMFAFailure(ip string) {
	if ip == "" {
		ip = "unknown"
	}
	_, failBucket, _ := authIPBuckets(ip, "mfa")
	s.noteAuthHit(failBucket)
}

func (s *Server) loginEmailLocked(email string) bool {
	return s.authHitsLimited(authEmailBucket(email), authEmailFailLimit, authEmailFailWindow)
}

func (s *Server) clearLoginFailures(ip, email string) {
	if ip == "" {
		ip = "unknown"
	}
	_, failBucket, _ := authIPBuckets(ip, "login")
	s.clearAuthHits(failBucket)
	_, mfaFail, _ := authIPBuckets(ip, "mfa")
	s.clearAuthHits(mfaFail)
	if email != "" {
		s.clearAuthHits(authEmailBucket(email))
	}
}

func (s *Server) authPageRateLimited(r *http.Request) bool {
	ip := clientIP(r)
	if ip == "" {
		ip = "unknown"
	}
	bucket := "auth:page:" + ip
	if s.authHitsLimited(bucket, authPageGetLimit, authPageGetWindow) {
		return true
	}
	s.noteAuthHit(bucket)
	return false
}

func honeypotFilled(v string) bool {
	return strings.TrimSpace(v) != ""
}
