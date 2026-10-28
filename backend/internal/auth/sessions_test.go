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
	b, _ := s.CreateSession(ctx, testUser, "ua-b", "2.2.2.2")
	time.Sleep(time.Millisecond)
	a, _ := s.CreateSession(ctx, testUser, "ua-a", "1.1.1.1")
	gone, _ := s.CreateSession(ctx, testUser, "ua", "ip")
	expired, _ := s.CreateSession(ctx, testUser, "ua", "ip")
	_, _ = s.CreateSession(ctx, otherUser, "ua", "ip")
	if err := s.EndSession(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET created_at = ? WHERE id = ?`, db.T(time.Now().Add(-2*time.Hour)), expired); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSessions(ctx, testUser, b)
	if err != nil {
		t.Fatal(err)
	}
	if ids := sessionIDs(list); len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("sessions %v, want [%s %s]", ids, a, b)
	}
	if x := list[0]; x.Current || x.UserAgent != "ua-a" || x.IPAddress != "1.1.1.1" {
		t.Fatalf("session: %+v", x)
	}
	if !list[1].Current {
		t.Fatal("current session not flagged")
	}
}

func TestRevokeSession(t *testing.T) {
	s := newService(t)
	addOtherUser(t, s)
	ctx := context.Background()
	revoked, a := login(t, s, testUser)
	kept, _ := login(t, s, testUser)
	_, theirs := login(t, s, otherUser)
	if err := s.RevokeSession(ctx, testUser, theirs); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked another user's session: %v", err)
	}
	if err := s.RevokeSession(ctx, testUser, a); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, testUser, a); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked twice: %v", err)
	}
	if _, err := s.Authenticate(ctx, revoked); err == nil {
		t.Fatal("revoked session still works")
	}
	if _, err := s.Authenticate(ctx, kept); err != nil {
		t.Fatalf("other session: %v", err)
	}
	if list, _ := s.ListSessions(ctx, otherUser, ""); len(list) != 1 {
		t.Fatalf("other user's sessions: %d", len(list))
	}
}

func TestRevokeOtherSessionsKeepsCurrent(t *testing.T) {
	s := newService(t)
	addOtherUser(t, s)
	ctx := context.Background()
	cur, _ := s.CreateSession(ctx, testUser, "ua", "ip")
	_, _ = s.CreateSession(ctx, testUser, "ua", "ip")
	_, _ = s.CreateSession(ctx, testUser, "ua", "ip")
	_, _ = s.CreateSession(ctx, otherUser, "ua", "ip")
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
	if rec := do("GET", "/api/v1/auth/me", laptop.AccessToken, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session: %d", rec.Code)
	}

	if rec := do("DELETE", "/api/v1/auth/sessions", me.AccessToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke others: %d %s", rec.Code, rec.Body)
	}
	if left := list(me.AccessToken); len(left) != 1 || left[0].ID != current || !left[0].Current {
		t.Fatalf("after revoke-all: %+v", left)
	}
	if rec := do("GET", "/api/v1/auth/me", phone.AccessToken, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked phone: %d", rec.Code)
	}
	if len(list(theirs.AccessToken)) != 1 {
		t.Fatal("revoke-all touched another user")
	}

	// Logout ends the session.
	if rec := do("POST", "/api/v1/auth/jwt/logout", me.AccessToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/api/v1/auth/me", me.AccessToken, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token after logout: %d", rec.Code)
	}
}
