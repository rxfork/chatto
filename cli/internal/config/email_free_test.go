package config

import "testing"

func TestEmailFreeConfigDoesNotRequireTransport(t *testing.T) {
	for _, transport := range []EmailTransport{EmailTransportSMTP, EmailTransportJMAP, "obsolete"} {
		cfg := validTestConfig()
		cfg.Email = EmailConfig{Disabled: true, Transport: transport}
		cfg.SMTP = SMTPConfig{Enabled: true}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("disabled %s: %v", transport, err)
		}
	}
	tls := validTestConfig()
	tls.Email.Disabled = true
	tls.Webserver.TLS = TLSConfig{Enabled: true, Domain: "chat.example.com"}
	if err := tls.Validate(); err != nil {
		t.Fatalf("email-free TLS: %v", err)
	}
	tls.Email.Disabled = false
	if err := tls.Validate(); err == nil {
		t.Fatal("default TLS email requirement changed")
	}
	cfg := validTestConfig()
	cfg.Email.Transport = EmailTransportJMAP
	if err := cfg.Validate(); err == nil {
		t.Fatal("default email validation disabled")
	}
	t.Setenv("CHATTO_EMAIL_DISABLED", "true")
	base := validTestConfig()
	t.Setenv("CHATTO_WEBSERVER_PORT", "4000")
	t.Setenv("CHATTO_WEBSERVER_COOKIE_SIGNING_SECRET", base.Webserver.CookieSigningSecret)
	t.Setenv("CHATTO_CORE_SECRET_KEY", base.Core.SecretKey)
	t.Setenv("CHATTO_CORE_ASSETS_SIGNING_SECRET", base.Core.Assets.SigningSecret)
	loaded, err := ReadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Email.Disabled {
		t.Fatal("environment switch not read")
	}
}
