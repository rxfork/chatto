package http_server

import (
	"bytes"
	"encoding/json"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hmans.de/chatto/internal/config"
	"hmans.de/chatto/internal/core"
)

func TestEmailFreeHTTPRegistrationAndAdmission(t *testing.T) {
	for _, policy := range []string{"open", "invite_only"} {
		t.Run(policy, func(t *testing.T) {
			ts, client, c := setupTestHTTPServerWithCoreConfig(t, config.CoreConfig{EmailDisabled: true, SkipSetupWizard: true}, func(s *HTTPServer) { s.config.Email.Disabled = true; s.config.Auth.AccountCreationPolicy = policy })
			ctx := testContext(t)
			post := func(path string, body map[string]string) int {
				t.Helper()
				bits, _ := json.Marshal(body)
				resp, err := client.Post(ts.URL+path, "application/json", bytes.NewReader(bits))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				io.Copy(io.Discard, resp.Body)
				return resp.StatusCode
			}
			signup := map[string]string{"login": "local-user", "password": "password123"}
			if policy == "invite_only" {
				if got := post("/auth/register/complete", signup); got != http.StatusBadRequest {
					t.Fatalf("uninvited status=%d", got)
				}
				admin, err := c.CreateUser(ctx, core.SystemActorID, "invite-owner", "Owner", "password123")
				if err != nil {
					t.Fatal(err)
				}
				if err := c.AssignAdminRole(ctx, admin.Id); err != nil {
					t.Fatal(err)
				}
				one := uint32(1)
				invite, err := c.CreateInvitation(ctx, admin.Id, &one, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Get(ts.URL + c.InvitationLinkPath(invite.ID))
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusSeeOther {
					t.Fatalf("invite status=%d", resp.StatusCode)
				}
			}
			if got := post("/auth/register/complete", signup); got != http.StatusOK {
				t.Fatalf("signup status=%d", got)
			}
			user, err := c.GetUserByLogin(ctx, "local-user")
			if err != nil {
				t.Fatal(err)
			}
			emails, err := c.GetVerifiedEmails(ctx, user.Id)
			if err != nil || len(emails) != 0 {
				t.Fatalf("emails=%v err=%v", emails, err)
			}
			if got := post("/auth/login", signup); got != http.StatusOK {
				t.Fatalf("login status=%d", got)
			}
			if policy == "invite_only" {
				signup["login"] = "second-user"
				if got := post("/auth/register/complete", signup); got != http.StatusBadRequest {
					t.Fatalf("reused invite=%d", got)
				}
			}
		})
	}
}

func TestEmailFreeHTTPRejectsEmailAndPreservesBrowserSafety(t *testing.T) {
	ts, client, _ := setupTestHTTPServerWithCoreConfig(t, config.CoreConfig{EmailDisabled: true, SkipSetupWizard: true}, func(s *HTTPServer) { s.config.Email.Disabled = true })
	for _, path := range []string{"/auth/register", "/auth/register/verify-code", "/auth/verify-email/request-code", "/auth/verify-email/confirm-code", "/auth/forgot-password", "/auth/reset-password"} {
		resp, err := client.Post(ts.URL+path, "application/json", strings.NewReader(`{"email":"unused@example.test","token":"old-token","password":"password123"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/auth/browser/register/complete", strings.NewReader(`{"login":"csrf-user","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site signup=%d", resp.StatusCode)
	}
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/auth/browser/register/complete", strings.NewReader(`{"login":"browser-user","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || body["token"] != nil {
		t.Fatalf("browser signup=%d %v", resp.StatusCode, body)
	}
}

func TestEmailFreeHTTPDirectRegistrationDisabled(t *testing.T) {
	disabled := false
	ts, client, _ := setupTestHTTPServerWithCoreConfig(t, config.CoreConfig{EmailDisabled: true, SkipSetupWizard: true}, func(s *HTTPServer) { s.config.Email.Disabled = true; s.config.Auth.DirectRegistration = &disabled })
	resp, err := client.Post(ts.URL+"/auth/register/complete", "application/json", strings.NewReader(`{"login":"no-user","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("signup status=%d", resp.StatusCode)
	}
}

func TestEmailFreeProviderScopesOverrideEmailRequests(t *testing.T) {
	for _, kind := range []string{"github", "gitlab", "google", "discord"} {
		t.Run(kind, func(t *testing.T) {
			enabled := true
			s := setupHTTPServerTestServer(t, config.AuthConfig{Providers: []config.AuthProviderConfig{{ID: "test", Type: kind, ClientID: "test-id", ClientSecret: "test-secret", RequestEmail: &enabled, Scopes: []string{"email", "user", "user:email", "https://www.googleapis.com/auth/userinfo.email"}}}})
			s.config.Email.Disabled = true
			s.router.Use(sessions.Sessions("test", cookie.NewStore([]byte("test-session-key"))))
			s.setupOIDCRoutes()
			response := httptest.NewRecorder()
			s.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/providers/test", nil))
			location, err := url.Parse(response.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			scopes := location.Query().Get("scope")
			if scopes == "" || strings.Contains(scopes, "email") || scopes == "user" {
				t.Fatalf("email-free %s scopes=%q", kind, scopes)
			}
		})
	}
}

func TestEmailFreeOIDCIgnoresUnsolicitedEmail(t *testing.T) {
	issuer := newNoEmailOIDCIssuer(t, "client-id")
	defer issuer.Close()
	issuer.tokenClaims = map[string]any{"email": "unsolicited@example.test", "email_verified": true}
	issuer.userInfoClaims = map[string]any{"sub": issuer.subject, "email": "fallback@example.test", "email_verified": true}
	runtime, err := newAuthProviderRuntime(config.AuthProviderConfig{ID: "test", Type: "oidc", IssuerURL: issuer.URL(), ClientID: "client-id", Scopes: emailFreeProviderScopes("oidc")}, "http://client.example/callback")
	if err != nil {
		t.Fatal(err)
	}
	runtime.emailDisabled = true
	router := gin.New()
	router.Use(sessions.Sessions("test", cookie.NewStore([]byte("test-session-key"))))
	router.GET("/callback", func(c *gin.Context) {
		if !runtime.ensureOIDC(c) {
			t.Fatal("provider initialization failed")
		}
		session := sessions.Default(c)
		session.Set(providerSessionKey("test", "code_verifier"), "test-verifier")
		identity, err := runtime.resolveOIDCIdentity(c, session)
		if err != nil {
			t.Fatal(err)
		}
		if identity.verifiedEmail != "" || identity.loginHint == "unsolicited" || identity.loginHint == "fallback" {
			t.Fatalf("email claim used: %+v", identity)
		}
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/callback?code=single-use-code", nil))
}
