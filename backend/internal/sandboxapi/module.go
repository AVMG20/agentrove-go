// Package sandboxapi serves /api/v1/sandbox/{sandbox_id}/... (files, git,
// search, zip download, the browser viewer's WebSocket) and the terminal
// WebSocket. Read-only operations run on the host against the workspace
// dir; mutations run inside the sandbox.
package sandboxapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/browserview"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

type Module struct {
	a       *app.App
	terms   *terminals
	viewers viewerCount
	// browserBox reaches a workspace's box browser and names the box (for
	// the viewer limit); tests replace it.
	browserBox func(ws *models.Workspace) (browserview.Box, string, error)
}

func New(a *app.App) *Module {
	m := &Module{a: a, terms: newTerminals(a.Sandboxes)}
	m.browserBox = m.dockerBrowser
	return m
}

// TerminateSandbox kills the sandbox's terminals and tmux sessions. Workspace
// deletion calls it while the sandbox still exists.
func (m *Module) TerminateSandbox(ctx context.Context, sandboxID string) {
	m.terms.terminateSandbox(ctx, sandboxID)
}

func (m *Module) Stop(ctx context.Context) { m.terms.closeAll() }

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/sandbox/{sandbox_id}"
	req := m.a.Auth.Require
	mux.HandleFunc("GET "+p+"/files/metadata", req(m.filesMetadata))
	mux.HandleFunc("GET "+p+"/files/content/{file_path...}", req(m.fileContent))
	mux.HandleFunc("GET "+p+"/files/box", req(m.boxFileContent))
	mux.HandleFunc("PUT "+p+"/files", req(m.updateFile))
	mux.HandleFunc("GET "+p+"/download-zip", req(m.downloadZip))
	mux.HandleFunc("GET "+p+"/git/diff", req(m.gitDiff))
	mux.HandleFunc("GET "+p+"/git/diff-stats", req(m.gitDiffStats))
	mux.HandleFunc("POST "+p+"/git/fetch-base", req(m.gitFetchBase))
	mux.HandleFunc("GET "+p+"/git/changed-paths", req(m.gitChangedPaths))
	mux.HandleFunc("GET "+p+"/git/file-baseline", req(m.gitFileBaseline))
	mux.HandleFunc("GET "+p+"/git/branches", req(m.gitBranches))
	mux.HandleFunc("POST "+p+"/git/checkout", req(m.gitCheckout))
	mux.HandleFunc("POST "+p+"/git/push", req(m.gitPush))
	mux.HandleFunc("POST "+p+"/git/pull", req(m.gitPull))
	mux.HandleFunc("POST "+p+"/git/commit", req(m.gitCommit))
	mux.HandleFunc("POST "+p+"/git/restore-file", req(m.gitRestoreFile))
	mux.HandleFunc("POST "+p+"/git/restore-all", req(m.gitRestoreAll))
	mux.HandleFunc("POST "+p+"/git/create-branch", req(m.gitCreateBranch))
	mux.HandleFunc("GET "+p+"/git/remote-url", req(m.gitRemoteURL))
	mux.HandleFunc("GET "+p+"/search", req(m.search))
	mux.HandleFunc("GET "+p+"/browser/ws", m.browserWS)
	mux.HandleFunc("POST "+p+"/browser/reset", req(m.browserReset))

	mux.HandleFunc("GET "+m.a.Cfg.APIPrefix+"/ws/{sandbox_id}/terminal", m.terminalWS)
}

// sandboxFor resolves the caller's workspace for the route's sandbox id.
func (m *Module) sandboxFor(r *http.Request) (*models.Workspace, *sandbox.Sandbox, error) {
	u := auth.UserFrom(r.Context())
	ws, err := m.a.Store.GetWorkspaceBySandbox(r.Context(), u.ID, r.PathValue("sandbox_id"))
	if err != nil {
		return nil, nil, err
	}
	sb, err := m.a.Sandboxes.For(ws)
	if err != nil {
		return nil, nil, httpx.BadRequest(err.Error())
	}
	return ws, sb, nil
}

// withCwd resolves the sandbox plus a validated workspace-relative cwd.
func (m *Module) withCwd(r *http.Request, cwd *string) (*sandbox.Sandbox, string, error) {
	_, sb, err := m.sandboxFor(r)
	if err != nil {
		return nil, "", err
	}
	raw := ""
	if cwd != nil {
		raw = *cwd
	}
	rel, err := gitops.ResolveCwd(sb, raw)
	if err != nil {
		return nil, "", err
	}
	return sb, rel, nil
}

func queryCwd(r *http.Request) *string {
	if v, ok := r.URL.Query()["cwd"]; ok && len(v) > 0 {
		return &v[0]
	}
	return nil
}

func queryBool(r *http.Request, key string) (bool, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return false, nil
	}
	switch v {
	case "true", "True", "1", "yes", "on":
		return true, nil
	case "false", "False", "0", "no", "off":
		return false, nil
	}
	return false, httpx.Unprocessable(fmt.Sprintf("Invalid boolean for %s", key))
}

func (m *Module) openRepo(r *http.Request) (*gitops.HostRepo, error) {
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return nil, err
	}
	repo, err := gitops.OpenHost(sb, rel)
	if err != nil {
		return nil, err
	}
	// Surface an unsafe repo as an error instead of an empty "not a repo" view.
	if err := repo.Verify(r.Context()); err != nil {
		return nil, err
	}
	return repo, nil
}

// ---- files ----

func (m *Module) filesMetadata(w http.ResponseWriter, r *http.Request) error {
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return err
	}
	items, err := listFiles(r.Context(), sb, rel)
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) {
			return err
		}
		return httpx.BadRequest(err.Error())
	}
	if rel != "" {
		for i := range items {
			items[i].Path = rel + "/" + items[i].Path
		}
	}
	return httpx.OK(w, map[string]any{"files": items})
}

func (m *Module) fileContent(w http.ResponseWriter, r *http.Request) error {
	_, sb, err := m.sandboxFor(r)
	if err != nil {
		return err
	}
	raw := r.PathValue("file_path")
	rel, err := gitops.NormalizeRel(raw)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	fc, err := readFile(sb, rel)
	if errors.Is(err, errTooLarge) {
		return httpx.Errorf(http.StatusRequestEntityTooLarge, "Failed to read file %s: %s", rel, err)
	}
	if err != nil {
		reason := err.Error()
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotAFile) {
			reason = "File not found: " + rel
		}
		return httpx.NotFound(fmt.Sprintf("Failed to read file %s: %s", rel, reason))
	}
	return httpx.OK(w, fc)
}

// boxFileContent reads a file by its absolute path in the box (?path=), for
// links agents write to files outside the project, such as their own plan
// and scratch dirs under HOME. It is read-only.
func (m *Module) boxFileContent(w http.ResponseWriter, r *http.Request) error {
	_, sb, err := m.sandboxFor(r)
	if err != nil {
		return err
	}
	raw := r.URL.Query().Get("path")
	fc, err := readBoxFile(sb, m.a.Cfg.AgentHomeDir(sb.UserID), raw)
	switch {
	case errors.Is(err, errNotAbsolute):
		return httpx.BadRequest(err.Error())
	case errors.Is(err, errTooLarge):
		return httpx.Errorf(http.StatusRequestEntityTooLarge, "Failed to read file %s: %s", raw, err)
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotAFile):
		return httpx.NotFound(fmt.Sprintf("Failed to read file %s: file not found", raw))
	case err != nil:
		return httpx.NotFound(fmt.Sprintf("Failed to read file %s: %s", raw, err))
	}
	return httpx.OK(w, fc)
}

var (
	errNotAbsolute = errors.New("path must be absolute")
	errOutsideHome = errors.New("only files in the box's home folder can be opened")
)

// readBoxFile reads the box file at the absolute path p: from the project
// when it is inside it, else from the box HOME on the host (agentHome),
// through an os.Root so a symlink can't lead out of it.
func readBoxFile(sb *sandbox.Sandbox, agentHome, p string) (FileContent, error) {
	clean := path.Clean(p)
	if !path.IsAbs(clean) || strings.ContainsRune(clean, 0) {
		return FileContent{}, errNotAbsolute
	}
	var data []byte
	var err error
	if rel, ok := strings.CutPrefix(clean, sb.Root+"/"); ok {
		data, err = readRaw(sb, rel)
	} else if rel, ok := strings.CutPrefix(clean, sb.Home+"/"); ok && sb.Home != "" {
		var root *os.Root
		if root, err = os.OpenRoot(agentHome); err == nil {
			data, err = readFrom(root, rel)
			root.Close()
		}
	} else {
		err = errOutsideHome
	}
	if err != nil {
		return FileContent{}, err
	}
	content, binary, lossy := EncodeContent(clean, data)
	return FileContent{Content: content, Path: p, Type: "file", IsBinary: binary, Lossy: lossy, Version: fileVersion(data)}, nil
}

func (m *Module) updateFile(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		FilePath *string `json:"file_path"`
		Content  *string `json:"content"`
		// BaseVersion is the version the edit started from; the save is
		// refused if the file changed on disk since. Empty skips the check.
		BaseVersion string `json:"base_version"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.FilePath == nil || *body.FilePath == "" || len(*body.FilePath) > 4096 {
		return httpx.Unprocessable("file_path must be 1-4096 characters")
	}
	if body.Content == nil {
		return httpx.Unprocessable("content is required")
	}
	_, sb, err := m.sandboxFor(r)
	if err != nil {
		return err
	}
	rel, err := gitops.NormalizeRel(*body.FilePath)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	if rel == "" {
		return httpx.BadRequest("Invalid file path")
	}
	version, err := saveFile(sb, rel, *body.Content, body.BaseVersion)
	if err != nil {
		return err
	}
	sb.Touch()
	return httpx.OK(w, map[string]any{"success": true, "message": "File " + rel + " updated successfully", "version": version})
}

func (m *Module) downloadZip(w http.ResponseWriter, r *http.Request) error {
	_, sb, err := m.sandboxFor(r)
	if err != nil {
		return err
	}
	// Build into a temp file so a failure still maps to an error status.
	tmp, err := os.CreateTemp("", "agentrove-zip-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := writeZip(r.Context(), sb, tmp); err != nil {
		return httpx.BadRequest(err.Error())
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="sandbox_`+sb.ID+`.zip"`)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, tmp)
	return nil
}

// ---- git (read-only, host) ----

func (m *Module) gitDiff(w http.ResponseWriter, r *http.Request) error {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "branch" {
		return httpx.Unprocessable("mode must be 'all' or 'branch'")
	}
	full, err := queryBool(r, "full_context")
	if err != nil {
		return err
	}
	repo, err := m.openRepo(r)
	if err != nil {
		return err
	}
	out, err := gitops.Diff(r.Context(), repo, mode, full)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitDiffStats(w http.ResponseWriter, r *http.Request) error {
	repo, err := m.openRepo(r)
	if err != nil {
		return err
	}
	out, err := gitops.DiffStats(r.Context(), repo)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitChangedPaths(w http.ResponseWriter, r *http.Request) error {
	repo, err := m.openRepo(r)
	if err != nil {
		return err
	}
	out, err := gitops.ChangedPaths(r.Context(), repo)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitFileBaseline(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("path")
	if p == "" || len(p) > 1024 {
		return httpx.Unprocessable("path must be 1-1024 characters")
	}
	repo, err := m.openRepo(r)
	if err != nil {
		return err
	}
	out, err := gitops.FileBaseline(r.Context(), repo, p)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitBranches(w http.ResponseWriter, r *http.Request) error {
	repo, err := m.openRepo(r)
	if err != nil {
		return err
	}
	out, err := gitops.Branches(r.Context(), repo)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitRemoteURL(w http.ResponseWriter, r *http.Request) error {
	repo, err := m.openRepo(r)
	if err != nil {
		return err
	}
	out, err := gitops.RemoteURL(r.Context(), repo)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) search(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	query := q.Get("q")
	if query == "" || len([]rune(query)) > 500 {
		return httpx.Unprocessable("q must be 1-500 characters")
	}
	include, exclude := q.Get("include"), q.Get("exclude")
	if len([]rune(include)) > 200 || len([]rune(exclude)) > 200 {
		return httpx.Unprocessable("include/exclude must be at most 200 characters")
	}
	opts := searchOptions{Query: query, Include: include, Exclude: exclude}
	var err error
	if opts.CaseSensitive, err = queryBool(r, "case_sensitive"); err != nil {
		return err
	}
	if opts.Regex, err = queryBool(r, "regex"); err != nil {
		return err
	}
	if opts.WholeWord, err = queryBool(r, "whole_word"); err != nil {
		return err
	}
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return err
	}
	out, err := search(r.Context(), sb, rel, opts)
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

// ---- git (mutations, in the sandbox) ----

func (m *Module) gitFetchBase(w http.ResponseWriter, r *http.Request) error {
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return err
	}
	out, err := gitops.FetchBase(r.Context(), sb, rel)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitPush(w http.ResponseWriter, r *http.Request) error {
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return err
	}
	out, err := gitops.Push(r.Context(), sb, rel)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitPull(w http.ResponseWriter, r *http.Request) error {
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return err
	}
	out, err := gitops.Pull(r.Context(), sb, rel)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitRestoreAll(w http.ResponseWriter, r *http.Request) error {
	sb, rel, err := m.withCwd(r, queryCwd(r))
	if err != nil {
		return err
	}
	out, err := gitops.RestoreAll(r.Context(), sb, rel)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitCheckout(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Branch string  `json:"branch"`
		Cwd    *string `json:"cwd"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Branch == "" || len(body.Branch) > 256 {
		return httpx.Unprocessable("branch must be 1-256 characters")
	}
	sb, rel, err := m.withCwd(r, body.Cwd)
	if err != nil {
		return err
	}
	out, err := gitops.Checkout(r.Context(), sb, rel, body.Branch)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitCommit(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Message string  `json:"message"`
		Cwd     *string `json:"cwd"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Message == "" || len([]rune(body.Message)) > 2000 {
		return httpx.Unprocessable("message must be 1-2000 characters")
	}
	sb, rel, err := m.withCwd(r, body.Cwd)
	if err != nil {
		return err
	}
	out, err := gitops.Commit(r.Context(), sb, rel, body.Message)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitRestoreFile(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		FilePath string  `json:"file_path"`
		OldPath  *string `json:"old_path"`
		Cwd      *string `json:"cwd"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.FilePath == "" || len(body.FilePath) > 4096 {
		return httpx.Unprocessable("file_path must be 1-4096 characters")
	}
	old := ""
	if body.OldPath != nil {
		if *body.OldPath == "" || len(*body.OldPath) > 4096 {
			return httpx.Unprocessable("old_path must be 1-4096 characters")
		}
		old = *body.OldPath
	}
	sb, rel, err := m.withCwd(r, body.Cwd)
	if err != nil {
		return err
	}
	out, err := gitops.RestoreFile(r.Context(), sb, rel, body.FilePath, old)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

func (m *Module) gitCreateBranch(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name       string  `json:"name"`
		BaseBranch *string `json:"base_branch"`
		Cwd        *string `json:"cwd"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Name == "" || len(body.Name) > 256 {
		return httpx.Unprocessable("name must be 1-256 characters")
	}
	sb, rel, err := m.withCwd(r, body.Cwd)
	if err != nil {
		return err
	}
	base := ""
	if body.BaseBranch != nil {
		base = *body.BaseBranch
	}
	out, err := gitops.CreateBranch(r.Context(), sb, rel, body.Name, base)
	if err != nil {
		return asBadRequest(err)
	}
	return httpx.OK(w, out)
}

// asBadRequest keeps typed HTTP errors and turns anything else (exec
// failures) into a 400 like the Python SandboxException mapping.
func asBadRequest(err error) error {
	var he *httpx.Error
	if errors.As(err, &he) {
		return err
	}
	return httpx.BadRequest(err.Error())
}
