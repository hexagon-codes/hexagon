package devui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	csrfHeader       = "X-DevUI-CSRF"
	sessionCookie    = "hexagon_devui_session"
	csrfCookie       = "hexagon_devui_csrf"
	devUISessionTTL  = 10 * time.Minute
	maxDevUISessions = 256
)

type devUISession struct {
	csrf      string
	origin    string
	expiresAt time.Time
}

func newAuthToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("devui: cannot initialize authentication token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func normalizeOrigins(origins []string) []string {
	seen := make(map[string]struct{}, len(origins))
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		normalized, ok := normalizeOrigin(origin)
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeOrigin(origin string) (string, bool) {
	origin = strings.TrimSpace(origin)
	if origin == "" || origin == "*" || origin == "null" {
		return "", false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func (d *DevUI) validateSecurityConfig() error {
	token := d.options.AuthToken
	if token != "" && (len(token) < 32 || strings.ContainsAny(token, " \t\r\n")) {
		return fmt.Errorf("devui: auth token must contain at least 32 non-whitespace bytes")
	}
	if !isLoopbackAddress(d.options.Addr) && token == "" {
		return fmt.Errorf("devui: non-loopback address %q requires an explicit auth token", d.options.Addr)
	}
	return nil
}

func isLoopbackAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	return isLoopbackHost(host)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestHostIsLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return isLoopbackHost(host)
}

func (d *DevUI) originAllowed(r *http.Request, origin string) bool {
	normalized, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if normalized == scheme+"://"+strings.ToLower(r.Host) {
		return true
	}
	for _, allowed := range d.options.AllowedOrigins {
		if candidate, valid := normalizeOrigin(allowed); valid && normalized == candidate {
			return true
		}
	}
	return false
}

func (d *DevUI) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
		}
		if d.options.CORSEnabled && origin != "" && d.originAllowed(r, origin) {
			normalized, _ := normalizeOrigin(origin)
			w.Header().Set("Access-Control-Allow-Origin", normalized)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, "+csrfHeader)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		} else if d.options.CORSEnabled && r.Method == http.MethodOptions && origin != "" {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		next(w, r)
	}
}

func (d *DevUI) protectBuilderWrites(apiPrefix string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isBuilderWriteRequest(apiPrefix, r) {
			next(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		bearerOK := validBearer(r.Header.Get("Authorization"), d.options.AuthToken)
		session, sessionOK := d.lookupSession(r)
		if !bearerOK && !sessionOK {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hexagon-devui"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if bearerOK {
			if !d.originAllowed(r, r.Header.Get("Origin")) {
				writeError(w, http.StatusForbidden, "origin not allowed")
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(d.options.AuthToken)) != 1 {
				writeError(w, http.StatusForbidden, "csrf validation failed")
				return
			}
		} else if !d.validSessionRequest(r, session) {
			writeError(w, http.StatusForbidden, "origin or csrf validation failed")
			return
		}
		next(w, r)
	}
}

func (d *DevUI) handleAuthBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !isLoopbackAddress(d.options.Addr) || !requestHostIsLoopback(r.Host) {
		writeError(w, http.StatusForbidden, "loopback bootstrap required")
		return
	}
	origin, ok := normalizeOrigin(r.Header.Get("Origin"))
	if !ok || !d.isStrictSameOrigin(r, origin) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}

	sessionID, csrf, expiresAt := d.createSession(origin)
	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(devUISessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    csrf,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(devUISessionTTL.Seconds()),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeSuccess(w, map[string]any{
		"csrf_token": csrf,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

func (d *DevUI) isStrictSameOrigin(r *http.Request, normalizedOrigin string) bool {
	if !requestHostIsLoopback(r.Host) {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return normalizedOrigin == scheme+"://"+strings.ToLower(r.Host)
}

func (d *DevUI) createSession(origin string) (sessionID, csrf string, expiresAt time.Time) {
	now := time.Now()
	expiresAt = now.Add(devUISessionTTL)
	d.sessionMu.Lock()
	defer d.sessionMu.Unlock()
	for id, session := range d.sessions {
		if !session.expiresAt.After(now) {
			delete(d.sessions, id)
		}
	}
	if len(d.sessions) >= maxDevUISessions {
		var oldestID string
		var oldestExpiry time.Time
		for id, session := range d.sessions {
			if oldestID == "" || session.expiresAt.Before(oldestExpiry) {
				oldestID = id
				oldestExpiry = session.expiresAt
			}
		}
		delete(d.sessions, oldestID)
	}
	for {
		sessionID = newAuthToken()
		if _, exists := d.sessions[sessionID]; !exists {
			break
		}
	}
	csrf = newAuthToken()
	d.sessions[sessionID] = devUISession{csrf: csrf, origin: origin, expiresAt: expiresAt}
	return sessionID, csrf, expiresAt
}

func (d *DevUI) lookupSession(r *http.Request) (devUISession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return devUISession{}, false
	}
	now := time.Now()
	d.sessionMu.Lock()
	defer d.sessionMu.Unlock()
	session, ok := d.sessions[cookie.Value]
	if !ok {
		return devUISession{}, false
	}
	if !session.expiresAt.After(now) {
		delete(d.sessions, cookie.Value)
		return devUISession{}, false
	}
	return session, true
}

func (d *DevUI) validSessionRequest(r *http.Request, session devUISession) bool {
	origin, ok := normalizeOrigin(r.Header.Get("Origin"))
	if !ok || origin != session.origin || !d.isStrictSameOrigin(r, origin) {
		return false
	}
	cookie, err := r.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(csrfHeader)
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(session.csrf)) == 1 &&
		subtle.ConstantTimeCompare([]byte(header), []byte(session.csrf)) == 1
}

func isBuilderWriteRequest(apiPrefix string, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	graphsPath := strings.TrimSuffix(apiPrefix, "/") + "/builder/graphs"
	return r.URL.Path == graphsPath || strings.HasPrefix(r.URL.Path, graphsPath+"/")
}

func validBearer(header, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || expected == "" {
		return false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func devUIURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
