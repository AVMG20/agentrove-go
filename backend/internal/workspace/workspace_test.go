package workspace

import "testing"

func TestNormalizeGitURL(t *testing.T) {
	ok := []string{"https://github.com/a/b.git", "git@github.com:a/b.git", "  https://gitlab.com/x/y  "}
	for _, u := range ok {
		if _, err := NormalizeGitURL(u); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
	bad := []string{"", "http://github.com/a/b", "https://user:tok@github.com/a/b", "file:///etc", "ssh://git@github.com/a/b"}
	for _, u := range bad {
		if _, err := NormalizeGitURL(u); err == nil {
			t.Errorf("%q should be rejected", u)
		}
	}
}

func TestRepoName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/widget.git":  "widget",
		"https://github.com/acme/widget/":     "widget",
		"git@github.com:acme/my.repo.git":     "my-repo",
		"https://github.com/":                 "workspace",
		"https://github.com/acme/we ird%20na": "we-ird-na",
	}
	for in, want := range cases {
		if got := RepoName(in); got != want {
			t.Errorf("RepoName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestSafeDirName(t *testing.T) {
	cases := map[string]string{
		"My Project": "My-Project",
		"../../etc":  "etc",
		"a/b":        "a-b",
		"...":        "workspace",
		"ok_name-1":  "ok_name-1",
	}
	for in, want := range cases {
		if got := SafeDirName(in); got != want {
			t.Errorf("SafeDirName(%q) = %q want %q", in, got, want)
		}
	}
	if id := NewSandboxID(); len(id) != 12 {
		t.Errorf("sandbox id %q", id)
	}
}
