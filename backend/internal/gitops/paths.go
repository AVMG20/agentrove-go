package gitops

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"
)

// NormalizeRel mirrors the Python normalize_relative_path: "" / "." / "./"
// mean the workspace root; absolutes, `..` segments, quotes and control
// characters are rejected so the value is safe in paths and shell scripts.
func NormalizeRel(p string) (string, error) {
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("Path must be workspace-relative, got absolute: %s", p)
	}
	p = strings.TrimPrefix(p, "./")
	if p == "" || p == "." {
		return "", nil
	}
	if strings.ContainsAny(p, "\x00\n\r'") || slices.Contains(strings.Split(p, "/"), "..") {
		return "", fmt.Errorf("Invalid relative path: %s", p)
	}
	return p, nil
}

// ResolveCwd validates a frontend `cwd` (workspace-relative, e.g.
// ".worktrees/abcd1234"). An absolute path inside the sandbox root is accepted
// too and turned relative. Errors are 400s.
func ResolveCwd(sb *sandbox.Sandbox, cwd string) (string, error) {
	if strings.HasPrefix(cwd, "/") {
		clean := path.Clean(cwd)
		if clean == sb.Root {
			return "", nil
		}
		if rel, ok := strings.CutPrefix(clean, sb.Root+"/"); ok {
			cwd = rel
		}
	}
	rel, err := NormalizeRel(cwd)
	if err != nil {
		return "", httpx.BadRequest(err.Error())
	}
	return rel, nil
}

var branchNameRE = regexp.MustCompile(`^[\w./-]+$`)

// ValidateBranchName rejects names that could inject shell or git syntax.
func ValidateBranchName(name, label string) error {
	if !branchNameRE.MatchString(name) || strings.Contains(name, "..") ||
		strings.Trim(name, ".") == "" || strings.HasPrefix(name, "-") {
		return httpx.BadRequest("Invalid " + label + " name")
	}
	return nil
}

// ValidateRelativePath guards file paths passed to git commands: no
// absolutes, no `..`, no option injection, no newlines/NULs.
func ValidateRelativePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") ||
		slices.Contains(strings.Split(p, "/"), "..") || strings.ContainsAny(p, "\n\x00") {
		return httpx.BadRequest("Invalid file path")
	}
	return nil
}

// shq single-quotes a value for POSIX shells.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
