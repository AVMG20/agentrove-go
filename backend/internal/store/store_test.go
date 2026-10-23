package store

import (
	"context"
	"strings"
	"testing"

	"agentrove/internal/auth"
	"agentrove/internal/db"
)

func TestUndecryptableGitHubTokenIsDropped(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	s := &Store{DB: d, Keys: keys}
	ctx := context.Background()
	const uid = "11111111-1111-1111-1111-111111111111"
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', ?, ?)`, uid, now, now); err != nil {
		t.Fatal(err)
	}

	enc, err := keys.Encrypt("ghp_secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUserSettings(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE user_settings SET github_personal_access_token = ? WHERE user_id = ?`, enc, uid); err != nil {
		t.Fatal(err)
	}
	st, err := s.GetUserSettings(ctx, uid)
	if err != nil || st.GithubPersonalAccessToken == nil || *st.GithubPersonalAccessToken != "ghp_secret" {
		t.Fatalf("token: %v %v", st.GithubPersonalAccessToken, err)
	}

	// The key changed: the stored value can no longer be opened.
	other, _ := auth.NewKeys(strings.Repeat("z", 40))
	s.Keys = other
	st, err = s.GetUserSettings(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if st.GithubPersonalAccessToken != nil {
		t.Fatalf("ciphertext leaked as token: %q", *st.GithubPersonalAccessToken)
	}
	if env := s.UserEnv(ctx, uid); env["GITHUB_TOKEN"] != "" || env["GH_TOKEN"] != "" {
		t.Fatalf("ciphertext exported to box env: %v", env)
	}
}
