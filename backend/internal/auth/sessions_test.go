package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentrove/internal/db"
)

const otherUser = "22222222-2222-2222-2222-222222222222"

func addOtherUser(t *testing.T, s *Service) {
	t.Helper()
	now := db.Now()
	if _, err := s.DB.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'o@b.c', 'o', 'x', 1, ?, ?)`, otherUser, now, now); err != nil {
		t.Fatal(err)
	}
}

func sessionIDs(list []Session) []string {
	ids := make([]string, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	return ids
}

func TestListSessionsOnlyActiveOfCaller(t *testing.T) {
	s := newService(t)
	addOtherUser(t, s)
	ctx := context.Background()
	tok, a, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua-a", "1.1.1.1")
	_, b, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua-b", "2.2.2.2")
	gone, _, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, expired, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, _, _ = s.CreateRefreshToken(ctx, s.DB, otherUser, "ua", "ip")
	if err := s.RevokeRefreshToken(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE refresh_tokens SET expires_at = ? WHERE session_id = ?`, db.T(time.Now().Add(-time.Minute)), expired); err != nil {
		t.Fatal(err)
	}
	// Rotation keeps the session: same id, created_at, one active row.
	var created db.Time
	_ = s.DB.QueryRow(`SELECT created_at FROM refresh_tokens WHERE session_id = ?`, a).Scan(&created)
	if _, _, sid, err := s.RotateRefreshToken(ctx, tok, "ua-a2", "3.3.3.3"); err != nil || sid != a {
		t.Fatalf("rotate: %q %v", sid, err)
	}
	list, err := s.ListSessions(ctx, testUser, b)
	if err != nil {
		t.Fatal(err)
	}
	if ids := sessionIDs(list); len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("sessions %v, want [%s %s]", ids, a, b)
	}
	if x := list[0]; x.Current || x.UserAgent != "ua-a2" || x.IPAddress != "3.3.3.3" || !x.CreatedAt.Equal(created.Time) || x.LastUsedAt.Before(created.Time) {
		t.Fatalf("rotated session: %+v (created %v)", x, created)
	}
	if !list[1].Current {
		t.Fatal("current session not flagged")
	}
}

func TestRevokeSession(t *testing.T) {
	s := newService(t)
	addOtherUser(t, s)
	ctx := context.Background()
	revoked, a, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	kept, b, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, theirs, _ := s.CreateRefreshToken(ctx, s.DB, otherUser, "ua", "ip")
	if err := s.RevokeSession(ctx, testUser, theirs); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked another user's session: %v", err)
	}
	if err := s.RevokeSession(ctx, testUser, a); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, testUser, a); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked twice: %v", err)
	}
	// The revoked device's refresh fails without signing out everyone else.
	if _, _, _, err := s.RotateRefreshToken(ctx, revoked, "ua", "ip"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("revoked refresh: %v", err)
	}
	if _, _, sid, err := s.RotateRefreshToken(ctx, kept, "ua", "ip"); err != nil || sid != b {
		t.Fatalf("other session: %q %v", sid, err)
	}
	if list, _ := s.ListSessions(ctx, otherUser, ""); len(list) != 1 {
		t.Fatalf("other user's sessions: %d", len(list))
	}
}

func TestRevokeOtherSessionsKeepsCurrent(t *testing.T) {
	s := newService(t)
	addOtherUser(t, s)
	ctx := context.Background()
	_, cur, _ := s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, _, _ = s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, _, _ = s.CreateRefreshToken(ctx, s.DB, testUser, "ua", "ip")
	_, _, _ = s.CreateRefreshToken(ctx, s.DB, otherUser, "ua", "ip")
	if err := s.RevokeOtherSessions(ctx, testUser, cur); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListSessions(ctx, testUser, cur); len(list) != 1 || list[0].ID != cur || !list[0].Current {
		t.Fatalf("left %v", sessionIDs(list))
	}
	if list, _ := s.ListSessions(ctx, otherUser, ""); len(list) != 1 {
		t.Fatalf("other user's sessions: %d", len(list))
	}
}

func TestSessionEndpoints(t *testing.T) {
	s := newService(t)
	addOtherUser(t, s)
	mux := http.NewServeMux()
	s.Routes(mux, "/api/v1")
	do := func(method, path, access, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if access != "" {
			req.Header.Set("Authorization", "Bearer "+access)
		}
		if strings.HasPrefix(body, "username=") {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	hash, _ := HashPassword("password1")
	if _, err := s.DB.Exec(`UPDATE users SET hashed_password = ?`, hash); err != nil {
		t.Fatal(err)
	}
	login := func(email string) tokenResponse {
		rec := do("POST", "/api/v1/auth/jwt/login", "", "username="+email+"&password=password1")
		if rec.Code != http.StatusOK {
			t.Fatalf("login: %d %s", rec.Code, rec.Body)
		}
		var tr tokenResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &tr)
		return tr
	}
	list := func(access string) []Session {
		rec := do("GET", "/api/v1/auth/sessions", access, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		var out []Session
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	me, laptop, phone, theirs := login("a@b.c"), login("a@b.c"), login("a@b.c"), login("o@b.c")

	sessions := list(me.AccessToken)
	var current, laptopID string
	for _, x := range sessions {
		if x.Current {
			current = x.ID
		}
	}
	for _, x := range list(laptop.AccessToken) {
		if x.Current {
			laptopID = x.ID
		}
	}
	if len(sessions) != 3 || current == "" || laptopID == "" || laptopID == current {
		t.Fatalf("sessions %+v", sessions)
	}
	if strings.Contains(do("GET", "/api/v1/auth/sessions", me.AccessToken, "").Body.String(), "token") {
		t.Fatal("token data in session list")
	}
	if rec := do("GET", "/api/v1/auth/sessions", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: %d", rec.Code)
	}

	theirID := list(theirs.AccessToken)[0].ID
	if rec := do("DELETE", "/api/v1/auth/sessions/"+theirID, me.AccessToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke another user's session: %d", rec.Code)
	}
	if rec := do("DELETE", "/api/v1/auth/sessions/"+current, me.AccessToken, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("revoke current session: %d", rec.Code)
	}
	if rec := do("DELETE", "/api/v1/auth/sessions/"+laptopID, me.AccessToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/v1/auth/jwt/refresh", "", `{"refresh_token":"`+laptop.RefreshToken+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session refresh: %d", rec.Code)
	}

	// The refreshed access token still identifies the same session.
	rec := do("POST", "/api/v1/auth/jwt/refresh", "", `{"refresh_token":"`+me.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec := do("DELETE", "/api/v1/auth/sessions", me.AccessToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke others: %d %s", rec.Code, rec.Body)
	}
	if left := list(me.AccessToken); len(left) != 1 || left[0].ID != current || !left[0].Current {
		t.Fatalf("after revoke-all: %+v", left)
	}
	if rec := do("POST", "/api/v1/auth/jwt/refresh", "", `{"refresh_token":"`+phone.RefreshToken+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked phone refresh: %d", rec.Code)
	}
	if rec := do("POST", "/api/v1/auth/jwt/refresh", "", `{"refresh_token":"`+me.RefreshToken+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("current session refresh after revoke-all: %d", rec.Code)
	}
	if len(list(theirs.AccessToken)) != 1 {
		t.Fatal("revoke-all touched another user")
	}

	// Access tokens from before session ids can't revoke everything else.
	legacy, _ := s.IssueAccessToken(otherUser)
	if rec := do("DELETE", "/api/v1/auth/sessions", legacy, ""); rec.Code != http.StatusConflict {
		t.Fatalf("legacy revoke-all: %d", rec.Code)
	}
}
