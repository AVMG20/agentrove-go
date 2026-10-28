package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentrove/internal/config"
	"agentrove/internal/db"
)

const testUser = "11111111-1111-1111-1111-111111111111"

func newService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	keys, _ := NewKeys(strings.Repeat("k", 40))
	cfg := &config.Config{SessionTTL: time.Hour, MCPTokenLifetime: time.Hour}
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', 1, ?, ?)`, testUser, now, now); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: d, Cfg: cfg, Keys: keys}
}

// login starts a session for userID and returns its token and id.
func login(t *testing.T, s *Service, userID string) (token, sessionID string) {
	t.Helper()
	sessionID, err := s.CreateSession(context.Background(), userID, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	token, err = s.IssueSessionToken(userID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return token, sessionID
}

func TestSessionTokenLivesWithItsSession(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	tok, sid := login(t, s, testUser)
	other, _ := login(t, s, testUser)
	if u, err := s.Authenticate(ctx, tok); err != nil || u.ID != testUser {
		t.Fatalf("authenticate: %v", err)
	}
	if err := s.EndSession(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tok); err == nil {
		t.Fatal("token works after its session ended")
	}
	if _, err := s.Authenticate(ctx, other); err != nil {
		t.Fatalf("other session ended too: %v", err)
	}
	// A token without a session (or for someone else's) is no login.
	bare, _ := s.Keys.IssueToken(testUser, AudienceSession, time.Hour)
	if _, err := s.Authenticate(ctx, bare); err == nil {
		t.Fatal("token without session accepted")
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, stale := login(t, s, testUser)
	fresh, _ := login(t, s, testUser)
	if _, err := s.DB.Exec(`UPDATE sessions SET created_at = ? WHERE id = ?`, db.T(time.Now().Add(-2*time.Hour)), stale); err != nil {
		t.Fatal(err)
	}
	s.PurgeExpiredSessions(ctx)
	if n := sessionCount(t, s); n != 1 {
		t.Fatalf("%d sessions after purge", n)
	}
	if _, err := s.Authenticate(ctx, fresh); err != nil {
		t.Fatalf("fresh session purged: %v", err)
	}
}

func sessionCount(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM sessions WHERE user_id = ?`, testUser).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestResetPasswordEndsSessions(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	tok, _ := login(t, s, testUser)
	reset := RandomToken(16)
	if _, err := s.DB.Exec(`UPDATE users SET reset_token = ?, reset_token_expires = ? WHERE id = ?`, HashToken(reset), db.T(time.Now().Add(time.Hour)), testUser); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ResetPassword(ctx, "wrong", "h"); ok || err != nil {
		t.Fatalf("bad token: %v %v", ok, err)
	}
	if n := sessionCount(t, s); n != 1 {
		t.Fatalf("bad token ended sessions: %d left", n)
	}
	if ok, err := s.ResetPassword(ctx, reset, "newhash"); !ok || err != nil {
		t.Fatalf("reset: %v %v", ok, err)
	}
	if _, err := s.Authenticate(ctx, tok); err == nil {
		t.Fatal("session survived reset")
	}
	u, _ := s.GetUser(ctx, testUser)
	if u.HashedPassword != "newhash" {
		t.Fatal("password not updated")
	}
	if ok, _ := s.ResetPassword(ctx, reset, "again"); ok {
		t.Fatal("reset token reusable")
	}
}

func TestMCPTokenScope(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	mcp, _ := s.IssueMCPToken(testUser)
	if _, err := s.Authenticate(ctx, mcp); err == nil {
		t.Fatal("MCP token accepted as a session")
	}
	if u, err := s.AuthenticateMCP(ctx, mcp); err != nil || u.ID != testUser {
		t.Fatalf("AuthenticateMCP: %v", err)
	}
	session, _ := login(t, s, testUser)
	if _, err := s.Authenticate(ctx, session); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := s.AuthenticateMCP(ctx, session); err == nil {
		t.Fatal("session token accepted as MCP token")
	}
}
