package auth

import (
	"context"
	"errors"
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
	cfg := &config.Config{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, MCPTokenLifetime: time.Hour}
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', 1, ?, ?)`, testUser, now, now); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: d, Cfg: cfg, Keys: keys}
}

func activeRefreshTokens(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM refresh_tokens WHERE user_id = ? AND revoked_at IS NULL`, testUser).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRefreshReuseRevokesAll(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	old, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	other, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, rotated, err := s.RotateRefreshToken(ctx, old, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RotateRefreshToken(ctx, old, "ua", "ip"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("reuse: %v", err)
	}
	if n := activeRefreshTokens(t, s); n != 0 {
		t.Fatalf("%d tokens survived reuse", n)
	}
	for _, tok := range []string{rotated, other} {
		if _, _, err := s.RotateRefreshToken(ctx, tok, "ua", "ip"); err == nil {
			t.Fatal("token still usable after reuse revoke")
		}
	}
}

func TestResetPasswordRevokesRefreshTokens(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, _ = s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	reset := RandomToken(16)
	if _, err := s.DB.Exec(`UPDATE users SET reset_token = ?, reset_token_expires = ? WHERE id = ?`, HashToken(reset), db.T(time.Now().Add(time.Hour)), testUser); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ResetPassword(ctx, "wrong", "h"); ok || err != nil {
		t.Fatalf("bad token: %v %v", ok, err)
	}
	if n := activeRefreshTokens(t, s); n != 1 {
		t.Fatalf("bad token revoked tokens: %d", n)
	}
	if ok, err := s.ResetPassword(ctx, reset, "newhash"); !ok || err != nil {
		t.Fatalf("reset: %v %v", ok, err)
	}
	if n := activeRefreshTokens(t, s); n != 0 {
		t.Fatalf("%d tokens survived reset", n)
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
	session, _ := s.IssueAccessToken(testUser)
	if _, err := s.Authenticate(ctx, session); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := s.AuthenticateMCP(ctx, session); err == nil {
		t.Fatal("session token accepted as MCP token")
	}
}
