package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"hmans.de/chatto/internal/config"
)

func emailFreeCore(t *testing.T) *ChattoCore {
	t.Helper()
	c, _ := newTestCore(t)
	c.config.EmailDisabled = true
	startCoreServices(t, c)
	return c
}

func TestEmailFreeRegistrationAndPasswordManagement(t *testing.T) {
	c := emailFreeCore(t)
	ctx := testContext(t)
	u, err := c.CreateLocalSignup(ctx, "noemail", "password123", "ignored", false)
	if err != nil {
		t.Fatal(err)
	}
	emails, err := c.GetVerifiedEmails(ctx, u.Id)
	if err != nil || len(emails) != 0 {
		t.Fatalf("emails=%v err=%v", emails, err)
	}
	if _, err := c.VerifyPassword(ctx, "noemail", "password123"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateLocalSignup(ctx, "NOEMAIL", "password123", "", false); !errors.Is(err, ErrLoginAlreadyTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := c.CreateLocalSignup(ctx, "badpassword", "short", "", false); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("short password: %v", err)
	}
	if err := c.AddVerifiedEmailDirect(ctx, u.Id, "notstored@example.test"); !errors.Is(err, ErrEmailDisabled) {
		t.Fatalf("add email: %v", err)
	}
	if err := c.SetOwnPassword(ctx, u.Id, "wrongpassword", "newpassword123"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := c.SetOwnPassword(ctx, u.Id, "password123", "newpassword123"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VerifyPassword(ctx, "noemail", "newpassword123"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AdminSetUserPasswordAs(ctx, SystemActorID, u.Id, "recovered123"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VerifyPassword(ctx, "noemail", "recovered123"); err != nil {
		t.Fatal(err)
	}
}

func TestEmailFreeInvitationConcurrentUsageAndFailures(t *testing.T) {
	c := emailFreeCore(t)
	ctx := testContext(t)
	admin := invitationAdmin(t, c)
	one := uint32(1)
	invite, err := c.CreateInvitation(ctx, admin, &one, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateLocalSignup(ctx, "uninvited", "password123", "", true); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("uninvited: %v", err)
	}
	if _, err := c.CreateLocalSignup(ctx, "invite-admin", "password123", invite.ID, true); !errors.Is(err, ErrLoginAlreadyTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	state, err := c.GetInvitation(ctx, admin, invite.ID)
	if err != nil || state.UseCount != 0 {
		t.Fatalf("failed signup consumed invite: %+v %v", state, err)
	}
	results := make(chan error, 4)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := c.CreateLocalSignup(ctx, fmt.Sprintf("race-%d", i), "password123", invite.ID, true)
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvitationInvalid) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
	state, err = c.GetInvitation(ctx, admin, invite.ID)
	if err != nil || state.UseCount != 1 {
		t.Fatalf("usage=%+v err=%v", state, err)
	}
	revoked, err := c.CreateInvitation(ctx, admin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.RevokeInvitation(ctx, admin, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateLocalSignup(ctx, "revoked", "password123", revoked.ID, true); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("revoked: %v", err)
	}
	expires := time.Now().Add(100 * time.Millisecond)
	expired, err := c.CreateInvitation(ctx, admin, nil, &expires)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the actual expiry; this is a core wall-clock expiration test.
	time.Sleep(time.Until(expires) + time.Millisecond)
	if _, err = c.CreateLocalSignup(ctx, "expired", "password123", expired.ID, true); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expired: %v", err)
	}
	open, err := c.CreateInvitation(ctx, admin, &one, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateLocalSignup(ctx, "open-user", "password123", open.ID, false); err != nil {
		t.Fatal(err)
	}
	state, err = c.GetInvitation(ctx, admin, open.ID)
	if err != nil || state.UseCount != 0 {
		t.Fatalf("open consumed invite: %+v %v", state, err)
	}
}

func TestEmailFreeOwnerBootstrapAndOperator(t *testing.T) {
	c, _ := newTestCore(t)
	c.config.EmailDisabled = true
	c.config.SkipSetupWizard = false
	startCoreServices(t, c)
	ctx := testContext(t)
	if _, err := c.CreateLocalSignup(ctx, "early-user", "password123", "", false); !errors.Is(err, ErrSetupRequired) {
		t.Fatalf("setup bypass: %v", err)
	}
	input := ServerSetupInput{ServerName: "Email-free", Login: "first-owner", DisplayName: "Owner", Password: "password123"}
	if err := c.CompleteServerSetup(ctx, input); err != nil {
		t.Fatal(err)
	}
	owner, err := c.GetUserByLogin(ctx, input.Login)
	if err != nil {
		t.Fatal(err)
	}
	if !c.isServerOwner(owner.Id) {
		t.Fatal("first user is not owner")
	}
	if err := c.CompleteServerSetup(ctx, input); !errors.Is(err, ErrSetupUnavailable) {
		t.Fatalf("setup reopened: %v", err)
	}
	view, err := c.AdminCreateUserAs(ctx, SystemActorID, AdminCreateUserRequest{Login: "operator-owner", Password: "password123", RoleNames: []string{RoleOwner}})
	if err != nil {
		t.Fatal(err)
	}
	if !c.isServerOwner(view.User.Id) || len(view.VerifiedEmails) != 0 {
		t.Fatalf("operator owner: %+v", view)
	}
	member, err := c.CreateLocalSignup(ctx, "ordinary-user", "password123", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AssignServerRoleToExistingUser(ctx, member.Id, member.Id, RoleOwner); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("self promotion: %v", err)
	}
}

func TestEmailFreeLimitCountsPasswordAccounts(t *testing.T) {
	c, _ := newTestCore(t)
	c.config.EmailDisabled = true
	ctx := testContext(t)
	max := 1
	c.config.Limits = config.LimitsConfig{MaxUsers: &max}
	startCoreServices(t, c)
	if _, err := c.CreateLocalSignup(ctx, "one-user", "password123", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateLocalSignup(ctx, "two-user", "password123", "", false); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("limit: %v", err)
	}
}

func TestEmailFreeModeRetainsExistingEmailAndLogin(t *testing.T) {
	c, nc := newTestCore(t)
	ctx := testContext(t)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(runCtx) }()
	var stopped sync.Once
	stop := func() { stopped.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	u, err := c.CreateVerifiedUser(ctx, SystemActorID, "existing-user", "Existing", "password123", "existing@example.test")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	cfg := c.config
	cfg.EmailDisabled = true
	c, err = NewChattoCore(ctx, nc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	startCoreServices(t, c)
	if _, err := c.VerifyPassword(ctx, "existing-user", "password123"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VerifyPassword(ctx, "existing@example.test", "password123"); err == nil {
		t.Fatal("email login accepted")
	}
	emails, err := c.GetVerifiedEmails(ctx, u.Id)
	if err != nil || len(emails) != 1 {
		t.Fatalf("existing email lost: %v %v", emails, err)
	}
}
