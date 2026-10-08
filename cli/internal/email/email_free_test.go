package email

import (
	"errors"
	"hmans.de/chatto/internal/config"
	"testing"
)

func TestEmailFreeSenderIgnoresTransport(t *testing.T) {
	for _, transport := range []config.EmailTransport{config.EmailTransportSMTP, config.EmailTransportJMAP} {
		sender := NewSender(config.EmailConfig{Disabled: true, Transport: transport}, config.SMTPConfig{Enabled: true, Host: "unreachable.invalid"})
		if sender.IsEnabled() {
			t.Fatal("disabled sender enabled")
		}
		if err := sender.Send(Message{To: "unused@example.test"}); !errors.Is(err, ErrEmailDisabled) {
			t.Fatal(err)
		}
	}
}
