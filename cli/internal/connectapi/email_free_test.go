package connectapi

import (
	"connectrpc.com/connect"
	apiv1 "hmans.de/chatto/internal/pb/chatto/api/v1"
	"testing"
)

func TestEmailFreeAccountRPCs(t *testing.T) {
	env := newConnectAPITestEnv(t)
	env.api.config.Email.Disabled = true
	s := &accountService{api: env.api}
	ctx := withCaller(env.ctx, env.viewer)
	calls := []func() error{
		func() error {
			_, err := s.ListVerifiedEmails(ctx, connect.NewRequest(&apiv1.ListVerifiedEmailsRequest{ExpectedUserId: env.viewer.Id}))
			return err
		},
		func() error {
			_, err := s.RequestEmailVerification(ctx, connect.NewRequest(&apiv1.RequestEmailVerificationRequest{ExpectedUserId: env.viewer.Id, Email: "unused@example.test"}))
			return err
		},
		func() error {
			_, err := s.ConfirmEmailVerification(ctx, connect.NewRequest(&apiv1.ConfirmEmailVerificationRequest{ExpectedUserId: env.viewer.Id, Email: "unused@example.test", Code: "old-code"}))
			return err
		},
		func() error {
			_, err := s.SetPrimaryEmail(ctx, connect.NewRequest(&apiv1.SetPrimaryEmailRequest{ExpectedUserId: env.viewer.Id, Email: "unused@example.test"}))
			return err
		},
	}
	for i, call := range calls {
		if err := call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
}
