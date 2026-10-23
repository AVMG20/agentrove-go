package config

import (
	"net/netip"
	"testing"
)

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes(" 10.0.0.0/8, 192.168.1.5 ,::1, 172.16.5.4/12,")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.5/32", "::1/128", "172.16.0.0/12"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("[%d] = %s, want %s", i, p, want[i])
		}
	}
	if !got[3].Contains(netip.MustParseAddr("172.20.0.3")) {
		t.Error("CIDR not masked")
	}
	if _, err := ParsePrefixes("10.0.0.0/8,nope"); err == nil {
		t.Error("invalid entry accepted")
	}
	if p, err := ParsePrefixes(""); err != nil || len(p) != 0 {
		t.Errorf("empty = %v, %v", p, err)
	}
}

func TestAgentGlobalEnvDropsDeploymentSecrets(t *testing.T) {
	t.Setenv("SECRET_KEY", "x")
	t.Setenv("ANTHROPIC_API_KEY", "k")
	c := &Config{
		AgentEnvPassthroughKey: []string{"SECRET_KEY", "ANTHROPIC_API_KEY"},
		AgentEnv:               map[string]string{"MAIL_PASSWORD": "p", "FOO": "bar"},
	}
	got := c.AgentGlobalEnv()
	if _, ok := got["SECRET_KEY"]; ok {
		t.Fatal("SECRET_KEY passed through")
	}
	if _, ok := got["MAIL_PASSWORD"]; ok {
		t.Fatal("MAIL_PASSWORD passed through")
	}
	if got["ANTHROPIC_API_KEY"] != "k" || got["FOO"] != "bar" {
		t.Fatalf("env = %v", got)
	}
}
