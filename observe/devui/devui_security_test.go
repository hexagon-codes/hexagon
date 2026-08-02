package devui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultOptions_BindLoopbackAndDisableCORS(t *testing.T) {
	opts := DefaultOptions()
	if opts.Addr != "127.0.0.1:8080" {
		t.Fatalf("default address = %q, want loopback-only 127.0.0.1:8080", opts.Addr)
	}
	if opts.CORSEnabled {
		t.Fatal("CORS must be disabled by default")
	}
}

func TestDevUI_URLHandlesExplicitLoopbackAddress(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{addr: "127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{addr: ":0", want: "http://localhost:0"},
	}
	for _, tt := range tests {
		if got := New(WithAddr(tt.addr)).URL(); got != tt.want {
			t.Fatalf("URL for %q = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestDevUI_CORSNeverEmitsWildcard(t *testing.T) {
	ui := New(WithCORS(true))
	req := httptest.NewRequest(http.MethodGet, "http://devui.local/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()

	ui.setupRoutes().ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Fatal("DevUI must never emit wildcard CORS")
	}
}

func TestCORSMiddleware_RejectsWildcardConfiguration(t *testing.T) {
	h := CORSMiddleware("*")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "http://devui.local/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Fatal("CORS helper must not emit a wildcard origin")
	}
}

func TestDevUI_CORSReflectsOnlyExplicitAllowedOrigin(t *testing.T) {
	ui := New(WithCORS(true), WithAllowedOrigins("https://trusted.example", "*"))
	h := ui.setupRoutes()

	for _, tt := range []struct {
		origin string
		want   string
	}{
		{origin: "https://trusted.example", want: "https://trusted.example"},
		{origin: "https://evil.example", want: ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://devui.local/health", nil)
		req.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != tt.want {
			t.Fatalf("origin %q: header = %q, want %q", tt.origin, got, tt.want)
		}
	}
}

func TestDevUI_ExecutionEndpointRequiresBearerOriginAndCSRF(t *testing.T) {
	const token = "configured-test-token"
	ui := New(WithAuthToken(token))
	h := ui.setupRoutes()

	tests := []struct {
		name       string
		auth       string
		origin     string
		csrf       string
		wantStatus int
	}{
		{name: "missing bearer", origin: "http://devui.local", csrf: token, wantStatus: http.StatusUnauthorized},
		{name: "wrong bearer", auth: "wrong", origin: "http://devui.local", csrf: token, wantStatus: http.StatusUnauthorized},
		{name: "missing origin", auth: token, csrf: token, wantStatus: http.StatusForbidden},
		{name: "foreign origin", auth: token, origin: "https://evil.example", csrf: token, wantStatus: http.StatusForbidden},
		{name: "missing csrf", auth: token, origin: "http://devui.local", wantStatus: http.StatusForbidden},
		{name: "wrong csrf", auth: token, origin: "http://devui.local", csrf: "wrong", wantStatus: http.StatusForbidden},
		{name: "authorized boundary reaches handler", auth: token, origin: "http://devui.local", csrf: token, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://devui.local/api/builder/graphs/missing/execute", strings.NewReader(`{}`))
			if tt.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tt.auth)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.csrf != "" {
				req.Header.Set("X-DevUI-CSRF", tt.csrf)
			}
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestDevUI_LoopbackBootstrapAllowsSameOriginUIExecution(t *testing.T) {
	ui := New()
	h := ui.setupRoutes()

	bootstrap := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/auth/bootstrap", nil)
	bootstrap.Header.Set("Origin", "http://127.0.0.1:8080")
	bw := httptest.NewRecorder()
	h.ServeHTTP(bw, bootstrap)
	if bw.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200; body=%s", bw.Code, bw.Body.String())
	}

	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range bw.Result().Cookies() {
		switch cookie.Name {
		case "hexagon_devui_session":
			sessionCookie = cookie
		case "hexagon_devui_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("bootstrap cookies missing: %v", bw.Result().Cookies())
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie flags are not hardened: %#v", sessionCookie)
	}
	if csrfCookie.HttpOnly || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("CSRF cookie must be readable by same-origin JS and SameSite=Strict: %#v", csrfCookie)
	}
	if sessionCookie.MaxAge <= 0 || sessionCookie.MaxAge > 15*60 {
		t.Fatalf("session lifetime must be short, got MaxAge=%d", sessionCookie.MaxAge)
	}

	execute := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/builder/graphs/missing/execute", strings.NewReader(`{}`))
	execute.Header.Set("Origin", "http://127.0.0.1:8080")
	execute.Header.Set("X-DevUI-CSRF", csrfCookie.Value)
	execute.AddCookie(sessionCookie)
	execute.AddCookie(csrfCookie)
	ew := httptest.NewRecorder()
	h.ServeHTTP(ew, execute)
	if ew.Code != http.StatusNotFound {
		t.Fatalf("same-origin bootstrapped UI did not reach execute handler: status=%d body=%s", ew.Code, ew.Body.String())
	}

	evilExecute := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/builder/graphs/missing/execute", strings.NewReader(`{}`))
	evilExecute.Header.Set("Origin", "https://evil.example")
	evilExecute.Header.Set("X-DevUI-CSRF", csrfCookie.Value)
	evilExecute.AddCookie(sessionCookie)
	evilExecute.AddCookie(csrfCookie)
	evilW := httptest.NewRecorder()
	h.ServeHTTP(evilW, evilExecute)
	if evilW.Code != http.StatusForbidden {
		t.Fatalf("foreign origin reused loopback session: status=%d body=%s", evilW.Code, evilW.Body.String())
	}

	ui.sessionMu.Lock()
	expired := ui.sessions[sessionCookie.Value]
	expired.expiresAt = time.Now().Add(-time.Second)
	ui.sessions[sessionCookie.Value] = expired
	ui.sessionMu.Unlock()
	expiredExecute := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/builder/graphs/missing/execute", strings.NewReader(`{}`))
	expiredExecute.Header.Set("Origin", "http://127.0.0.1:8080")
	expiredExecute.Header.Set("X-DevUI-CSRF", csrfCookie.Value)
	expiredExecute.AddCookie(sessionCookie)
	expiredExecute.AddCookie(csrfCookie)
	expiredW := httptest.NewRecorder()
	h.ServeHTTP(expiredW, expiredExecute)
	if expiredW.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status=%d, want 401", expiredW.Code)
	}
}

func TestDevUI_LoopbackBootstrapAndSessionRejectForeignOrigin(t *testing.T) {
	ui := New()
	h := ui.setupRoutes()

	for _, tt := range []struct {
		name   string
		url    string
		origin string
	}{
		{name: "foreign origin", url: "http://127.0.0.1:8080/api/auth/bootstrap", origin: "https://evil.example"},
		{name: "dns rebinding host", url: "http://evil.example/api/auth/bootstrap", origin: "http://evil.example"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bootstrap := httptest.NewRequest(http.MethodPost, tt.url, nil)
			bootstrap.Header.Set("Origin", tt.origin)
			bw := httptest.NewRecorder()
			h.ServeHTTP(bw, bootstrap)
			if bw.Code != http.StatusForbidden {
				t.Fatalf("bootstrap status = %d, want 403", bw.Code)
			}
			if len(bw.Result().Cookies()) != 0 {
				t.Fatalf("untrusted origin received session cookies: %v", bw.Result().Cookies())
			}
		})
	}
}

func TestDevUI_NonLoopbackRequiresExplicitTokenBeforeListen(t *testing.T) {
	ui := New(WithAddr("0.0.0.0:0"))
	if ui.options.AuthToken != "" {
		t.Fatal("non-loopback DevUI must not auto-generate or expose a bearer token")
	}

	result := make(chan error, 1)
	go func() { result <- ui.Start() }()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "auth token") {
			t.Fatalf("Start error = %v, want missing auth token error", err)
		}
	case <-time.After(500 * time.Millisecond):
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = ui.Stop(ctx)
		<-result
		t.Fatal("non-loopback DevUI listened without an explicit auth token")
	}
}

func TestDevUI_NonLoopbackRejectsWeakTokenBeforeListen(t *testing.T) {
	ui := New(WithAddr("0.0.0.0:0"), WithAuthToken("short-token"))
	result := make(chan error, 1)
	go func() { result <- ui.Start() }()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "at least 32") {
			t.Fatalf("Start error = %v, want weak auth token error", err)
		}
	case <-time.After(500 * time.Millisecond):
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = ui.Stop(ctx)
		<-result
		t.Fatal("non-loopback DevUI listened with a weak auth token")
	}
}

func TestDevUI_BuilderWritesRejectMissingSessionAndForeignOrigin(t *testing.T) {
	writes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create", method: http.MethodPost, path: "/api/builder/graphs", body: `{"name":"blocked"}`},
		{name: "update", method: http.MethodPut, path: "/api/builder/graphs/missing", body: `{"name":"blocked"}`},
		{name: "delete", method: http.MethodDelete, path: "/api/builder/graphs/missing"},
		{name: "validate", method: http.MethodPost, path: "/api/builder/graphs/missing/validate"},
		{name: "execute", method: http.MethodPost, path: "/api/builder/graphs/missing/execute", body: `{}`},
	}
	for _, tt := range writes {
		t.Run("missing session "+tt.name, func(t *testing.T) {
			ui := New()
			req := httptest.NewRequest(tt.method, "http://127.0.0.1:8080"+tt.path, strings.NewReader(tt.body))
			req.Header.Set("Origin", "http://127.0.0.1:8080")
			w := httptest.NewRecorder()
			ui.setupRoutes().ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d, want 401; body=%s", w.Code, w.Body.String())
			}
		})
	}

	const token = "0123456789abcdef0123456789abcdef"
	ui := New(WithAuthToken(token))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/builder/graphs", strings.NewReader(`{"name":"blocked"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-DevUI-CSRF", token)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	ui.setupRoutes().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin create status=%d, want 403; body=%s", w.Code, w.Body.String())
	}
	if got := len(ui.graphStore.List()); got != 0 {
		t.Fatalf("foreign-origin request mutated graph store: count=%d", got)
	}
}

func TestDevUI_LoopbackBootstrapSessionSupportsBuilderCRUD(t *testing.T) {
	ui := New()
	h := ui.setupRoutes()
	bootstrap := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/auth/bootstrap", nil)
	bootstrap.Header.Set("Origin", "http://127.0.0.1:8080")
	bw := httptest.NewRecorder()
	h.ServeHTTP(bw, bootstrap)
	if bw.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", bw.Code, bw.Body.String())
	}
	var session, csrf *http.Cookie
	for _, cookie := range bw.Result().Cookies() {
		switch cookie.Name {
		case "hexagon_devui_session":
			session = cookie
		case "hexagon_devui_csrf":
			csrf = cookie
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("bootstrap cookies missing")
	}
	doWrite := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-DevUI-CSRF", csrf.Value)
		req.AddCookie(session)
		req.AddCookie(csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	created := doWrite(http.MethodPost, "/api/builder/graphs", `{"name":"session graph"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	graphs := ui.graphStore.List()
	if len(graphs) != 1 {
		t.Fatalf("created graph count=%d, want 1", len(graphs))
	}
	id := graphs[0].ID
	if updated := doWrite(http.MethodPut, "/api/builder/graphs/"+id, `{"name":"updated"}`); updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	if validated := doWrite(http.MethodPost, "/api/builder/graphs/"+id+"/validate", ""); validated.Code != http.StatusOK {
		t.Fatalf("validate status=%d body=%s", validated.Code, validated.Body.String())
	}
	if deleted := doWrite(http.MethodDelete, "/api/builder/graphs/"+id, ""); deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}
