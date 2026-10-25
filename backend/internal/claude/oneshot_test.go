package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCLI reports which login it was given and what it can see, and imitates
// the real CLI's writes into its config dir (and, with ANTHROPIC_TEST_ROTATE,
// an OAuth refresh rewriting .credentials.json).
const fakeCLI = `#!/bin/sh
cat >/dev/null
c=none
grep -q SECRET-A "$CLAUDE_CONFIG_DIR/.credentials.json" 2>/dev/null && c=A
grep -q VICTIM "$CLAUDE_CONFIG_DIR/.credentials.json" 2>/dev/null && c=victim
m=clean
grep -q mcpServers "$CLAUDE_CONFIG_DIR/.claude.json" 2>/dev/null && m=leaked
a=noaccount
grep -q acct-1 "$CLAUDE_CONFIG_DIR/.claude.json" 2>/dev/null && a=account
echo x > "$CLAUDE_CONFIG_DIR/remote-settings.json"
if [ -n "$ANTHROPIC_TEST_ROTATE" ]; then
  printf '{"claudeAiOauth":{"accessToken":"ROTATED"}}' > "$CLAUDE_CONFIG_DIR/.credentials.json"
fi
printf '{"result":"%s|%s|%s|%s"}' "$c" "$m" "$a" "$CLAUDE_CONFIG_DIR"
`

func writeFakeCLI(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte(fakeCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// homes lays out two users' agent homes like storage/agent-homes/<uid>.
func homes(t *testing.T) (user, victim string) {
	t.Helper()
	base := t.TempDir()
	user = filepath.Join(base, "agent-homes", "u1")
	victim = filepath.Join(base, "agent-homes", "u2")
	mustWrite(t, filepath.Join(victim, "claude", credentialsFile), `{"claudeAiOauth":{"accessToken":"VICTIM"}}`)
	if err := os.MkdirAll(user, 0o755); err != nil {
		t.Fatal(err)
	}
	return user, victim
}

func runOneShot(t *testing.T, o OneShot) []string {
	t.Helper()
	out, err := o.Run(context.Background(), "haiku", "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(out, "|")
	if len(parts) != 4 {
		t.Fatalf("unexpected output %q", out)
	}
	return parts
}

func TestOneShotUsesPrivateCopyOfLogin(t *testing.T) {
	user, _ := homes(t)
	cfg := ConfigDirFor(user)
	mustWrite(t, filepath.Join(cfg, credentialsFile), `{"claudeAiOauth":{"accessToken":"SECRET-A"}}`)
	mustWrite(t, filepath.Join(cfg, globalConfigFile), `{"oauthAccount":{"accountUuid":"acct-1"},"mcpServers":{"x":{"command":"evil"}}}`)

	got := runOneShot(t, OneShot{Binary: writeFakeCLI(t), ConfigDir: cfg})
	if got[0] != "A" || got[2] != "account" {
		t.Errorf("login not staged: %v", got)
	}
	if got[1] != "clean" {
		t.Error("agent-written .claude.json keys beyond the login reached the CLI")
	}
	if got[3] == cfg || strings.HasPrefix(got[3], cfg) {
		t.Errorf("CLI ran against the box-writable config dir %s", got[3])
	}
	if _, err := os.Stat(filepath.Join(cfg, "remote-settings.json")); err == nil {
		t.Error("CLI wrote into the user's config dir")
	}
}

func TestOneShotWritesBackRotatedLogin(t *testing.T) {
	user, _ := homes(t)
	cfg := ConfigDirFor(user)
	credPath := filepath.Join(cfg, credentialsFile)
	mustWrite(t, credPath, `{"claudeAiOauth":{"accessToken":"SECRET-A"}}`)

	runOneShot(t, OneShot{Binary: writeFakeCLI(t), ConfigDir: cfg, Env: map[string]string{"ANTHROPIC_TEST_ROTATE": "1"}})
	b, err := os.ReadFile(credPath)
	if err != nil || !strings.Contains(string(b), "ROTATED") {
		t.Fatalf("rotated login not written back: %q %v", b, err)
	}
	fi, err := os.Lstat(credPath)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Errorf("written-back login: %v %v", fi.Mode(), err)
	}
}

// The agent can make ~/claude or the files in it symlinks to another user's
// login; the host must neither use nor overwrite it.
func TestOneShotRefusesSymlinkedLogin(t *testing.T) {
	t.Run("credentials file", func(t *testing.T) {
		user, victim := homes(t)
		cfg := ConfigDirFor(user)
		if err := os.MkdirAll(cfg, 0o755); err != nil {
			t.Fatal(err)
		}
		victimCreds := filepath.Join(victim, "claude", credentialsFile)
		if err := os.Symlink(victimCreds, filepath.Join(cfg, credentialsFile)); err != nil {
			t.Fatal(err)
		}
		got := runOneShot(t, OneShot{Binary: writeFakeCLI(t), ConfigDir: cfg, Env: map[string]string{"ANTHROPIC_TEST_ROTATE": "1"}})
		if got[0] == "victim" {
			t.Fatal("one-shot used another user's login through a symlink")
		}
		if b, _ := os.ReadFile(victimCreds); !strings.Contains(string(b), "VICTIM") {
			t.Errorf("victim login was overwritten: %q", b)
		}
	})
	t.Run("config dir", func(t *testing.T) {
		user, victim := homes(t)
		cfg := ConfigDirFor(user)
		if err := os.Symlink(filepath.Join(victim, "claude"), cfg); err != nil {
			t.Fatal(err)
		}
		_, err := OneShot{Binary: writeFakeCLI(t), ConfigDir: cfg}.Run(context.Background(), "haiku", "", "hi")
		if err == nil {
			t.Error("a symlinked config dir should be refused")
		}
		if _, err := os.Stat(filepath.Join(victim, "claude", "remote-settings.json")); err == nil {
			t.Error("CLI wrote into another user's config dir")
		}
	})
}

// A profile's config dir is <home>/.agentrove/profiles/<id>/claude; the box
// can replace the profile dir with a symlink to another user's home, which
// must neither be read nor written.
func TestOneShotRefusesSymlinkedProfileDir(t *testing.T) {
	user, victim := homes(t)
	profiles := filepath.Join(user, ".agentrove", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	// The victim's home stands in for the profile dir: its claude/ holds the
	// victim's login.
	if err := os.Symlink(victim, filepath.Join(profiles, "p1")); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(profiles, "p1", "claude")
	_, err := OneShot{Binary: writeFakeCLI(t), ConfigDir: cfg, AgentHome: user,
		Env: map[string]string{"ANTHROPIC_TEST_ROTATE": "1"}}.Run(context.Background(), "haiku", "", "hi")
	if err == nil {
		t.Error("a profile dir symlinked out of the home should be refused")
	}
	if b, _ := os.ReadFile(filepath.Join(victim, "claude", credentialsFile)); !strings.Contains(string(b), "VICTIM") {
		t.Errorf("victim login was overwritten: %q", b)
	}

	// A real profile dir in the home works and uses its own login.
	own := filepath.Join(profiles, "p2", "claude")
	mustWrite(t, filepath.Join(own, credentialsFile), `{"claudeAiOauth":{"accessToken":"SECRET-A"}}`)
	if got := runOneShot(t, OneShot{Binary: writeFakeCLI(t), ConfigDir: own, AgentHome: user}); got[0] != "A" {
		t.Fatalf("profile login not used: %q", got)
	}
}

// A refresh the box did while the one-shot ran must not be clobbered.
func TestWriteBackKeepsConcurrentBoxRefresh(t *testing.T) {
	user, _ := homes(t)
	cfg := ConfigDirFor(user)
	credPath := filepath.Join(cfg, credentialsFile)
	mustWrite(t, credPath, `{"claudeAiOauth":{"accessToken":"SECRET-A"}}`)
	priv := t.TempDir()
	login, err := stageLogin("", cfg, priv)
	if err != nil || login == nil {
		t.Fatal(err)
	}
	mustWrite(t, credPath, `{"claudeAiOauth":{"accessToken":"BOX-REFRESHED"}}`)
	mustWrite(t, filepath.Join(priv, credentialsFile), `{"claudeAiOauth":{"accessToken":"ROTATED"}}`)
	if err := login.writeBack(priv); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(credPath); !strings.Contains(string(b), "BOX-REFRESHED") {
		t.Errorf("box's concurrent refresh was clobbered: %q", b)
	}
}

func TestOneShotEnvCredentialsSkipFiles(t *testing.T) {
	user, _ := homes(t)
	cfg := ConfigDirFor(user)
	mustWrite(t, filepath.Join(cfg, credentialsFile), `{"claudeAiOauth":{"accessToken":"SECRET-A"}}`)
	got := runOneShot(t, OneShot{Binary: writeFakeCLI(t), ConfigDir: cfg, Env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"}})
	if got[0] != "none" {
		t.Errorf("login files staged although the env has credentials: %v", got)
	}
}
