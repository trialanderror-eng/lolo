package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/webhook/alertmanager", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/investigate", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func TestAuthMiddleware_investigateUsesBearerToken(t *testing.T) {
	h := authMiddleware("s3cret", "", newMux())

	// Missing bearer on /investigate → 401
	req := httptest.NewRequest(http.MethodPost, "/investigate", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /investigate = %d, want 401", w.Code)
	}

	// Correct bearer → 202 (through to handler)
	req = httptest.NewRequest(http.MethodPost, "/investigate", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("good bearer /investigate = %d, want 202", w.Code)
	}
}

func TestAuthMiddleware_healthAlwaysOpen(t *testing.T) {
	for _, token := range []string{"", "secret"} {
		h := authMiddleware(token, token, newMux())
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("token=%q: /healthz = %d, want 200", token, w.Code)
		}
	}
}

func TestAuthMiddleware_dashboardBasicAuth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/investigations", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("dev mode allows unauth", func(t *testing.T) {
		h := authMiddleware("", "", mux)
		for _, p := range []string{"/", "/api/investigations"} {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s = %d, want 200 (dev mode)", p, w.Code)
			}
		}
	})

	t.Run("missing creds → 401 with Basic challenge", func(t *testing.T) {
		h := authMiddleware("", "dashpw", mux)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", w.Code)
		}
		if !strings.Contains(w.Header().Get("WWW-Authenticate"), "Basic") {
			t.Errorf("missing Basic challenge: %q", w.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("correct password accepts (any username)", func(t *testing.T) {
		h := authMiddleware("", "dashpw", mux)
		req := httptest.NewRequest(http.MethodGet, "/api/investigations", nil)
		req.SetBasicAuth("anyuser", "dashpw")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("got %d, want 200", w.Code)
		}
	})

	t.Run("wrong password → 401", func(t *testing.T) {
		h := authMiddleware("", "dashpw", mux)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("u", "wrong")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", w.Code)
		}
	})
}

func TestAuthMiddleware_emptyTokenAllowsWebhook(t *testing.T) {
	h := authMiddleware("", "", newMux())
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("dev mode: /webhook = %d, want 202 (handler accepts)", w.Code)
	}
}

func TestAuthMiddleware_correctBearerAccepts(t *testing.T) {
	h := authMiddleware("s3cret", "", newMux())
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("good token: /webhook = %d, want 202", w.Code)
	}
}

func TestAuthMiddleware_rejectsBadAndMissingTokens(t *testing.T) {
	cases := []struct {
		name, header string
	}{
		{"missing header", ""},
		{"wrong scheme", "Basic s3cret"},
		{"wrong token", "Bearer wrong"},
		{"empty bearer", "Bearer "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := authMiddleware("s3cret", "", newMux())
			req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(`{}`))
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("got %d, want 401", w.Code)
			}
			if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want Bearer challenge", got)
			}
		})
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , , b ", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}
