package channel

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"

	"github.com/google/uuid"
)

// savedFile is an uploaded attachment before it's tied to a channel message.
type savedFile struct {
	FileURL  string `json:"file_url"`
	FilePath string `json:"file_path"`
	FileType string `json:"file_type"`
	Filename string `json:"filename"`
}

var allowedImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

const xlsxType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// claudeNativeTypes are passed to Claude inline; others are copied into the
// workspace so the agent can read them.
var claudeNativeTypes = map[string]bool{"image": true, "pdf": true}

// saveFiles stores uploads under STORAGE_PATH/temp/<user>/ like the chat
// upload path, and copies non-native types into the workspace root.
func (s *Service) saveFiles(ctx context.Context, ch *Channel, files []*multipart.FileHeader) ([]savedFile, error) {
	if len(files) == 0 {
		return nil, nil
	}
	var sb *sandbox.Sandbox
	out := make([]savedFile, 0, len(files))
	for _, fh := range files {
		ct := fh.Header.Get("Content-Type")
		var folder, fileType string
		switch {
		case allowedImageTypes[ct]:
			folder, fileType = "images", "image"
		case ct == "application/pdf":
			folder, fileType = "pdfs", "pdf"
		case ct == xlsxType:
			folder, fileType = "xlsx", "xlsx"
		default:
			return nil, httpx.BadRequest("Invalid file type: " + ct)
		}
		if fh.Filename == "" {
			return nil, httpx.BadRequest("Filename is required")
		}
		if fh.Size > s.a.Cfg.MaxUploadSize {
			return nil, httpx.BadRequest(fmt.Sprintf("File too large: %d bytes exceeds %d bytes", fh.Size, s.a.Cfg.MaxUploadSize))
		}
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, s.a.Cfg.MaxUploadSize+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > s.a.Cfg.MaxUploadSize {
			return nil, httpx.BadRequest(fmt.Sprintf("File too large: %d bytes exceeds %d bytes", len(data), s.a.Cfg.MaxUploadSize))
		}
		unique := uuid.NewString() + strings.ToLower(filepath.Ext(fh.Filename))
		rel := filepath.ToSlash(filepath.Join("temp", ch.UserID, folder, unique))
		abs := filepath.Join(s.a.Cfg.StoragePath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return nil, err
		}
		if !claudeNativeTypes[fileType] {
			if sb == nil {
				if sb, err = s.sandboxFor(ctx, ch); err != nil {
					return nil, err
				}
			}
			root, err := sb.HostPath("")
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(root, unique), data, 0o644); err != nil {
				return nil, httpx.Errorf(500, "Failed to copy attachment into the workspace: %v", err)
			}
		}
		out = append(out, savedFile{
			FileURL:  s.a.Cfg.APIPrefix + "/attachments/temp/preview?path=" + rel,
			FilePath: rel, FileType: fileType, Filename: fh.Filename,
		})
	}
	return out, nil
}

func (s *Service) sandboxFor(ctx context.Context, ch *Channel) (*sandbox.Sandbox, error) {
	ws, err := s.a.Store.GetWorkspaceByID(ctx, ch.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return s.a.Sandboxes.For(ws)
}

const gitDiscoveryEnv = "export GIT_DISCOVERY_ACROSS_FILESYSTEM=1 && "

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// checkout switches the shared workspace to branch (non-worktree channels).
func checkout(ctx context.Context, sb *sandbox.Sandbox, branch string) error {
	q := shellQuote(branch)
	res, err := sb.Shell(ctx, "", gitDiscoveryEnv+"git checkout "+q+" 2>&1", time.Minute)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	if res.ExitCode != 0 {
		local, _ := sb.Shell(ctx, "", gitDiscoveryEnv+"git show-ref --verify --quiet refs/heads/"+q, time.Minute)
		if local.ExitCode == 1 {
			res, err = sb.Shell(ctx, "", gitDiscoveryEnv+"git checkout -b "+q+" "+shellQuote("origin/"+branch)+" 2>&1", time.Minute)
			if err != nil {
				return httpx.BadRequest(err.Error())
			}
		}
	}
	head, _ := sb.Shell(ctx, "", gitDiscoveryEnv+"git rev-parse --abbrev-ref HEAD 2>/dev/null", time.Minute)
	current := strings.TrimSpace(head.Stdout)
	if res.ExitCode != 0 && current != branch {
		msg := strings.TrimSpace(res.Stdout)
		if msg == "" {
			msg = strings.TrimSpace(res.Stderr)
		}
		return httpx.BadRequest(msg)
	}
	if current == "HEAD" {
		_, _ = sb.Shell(ctx, "", gitDiscoveryEnv+"git checkout - 2>/dev/null", time.Minute)
		return httpx.BadRequest("Cannot checkout: would result in detached HEAD")
	}
	return nil
}

// createWorktree is idempotent and hides .worktrees/ from the parent checkout;
// the returned cwd is workspace-relative (".worktrees/<short id>").
func createWorktree(ctx context.Context, sb *sandbox.Sandbox, chatID string, baseRef *string) (string, error) {
	short := chatID[:8]
	dir := ".worktrees/" + short
	branch := "worktree-" + short
	preamble := "git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 2; " +
		"if [ -e '" + dir + "/.git' ]; then echo 'exists'; exit 0; fi; " +
		`excl="$(git rev-parse --git-path info/exclude)"; ` +
		`{ mkdir -p "${excl%/*}" && grep -qxF ".worktrees/" "$excl" || echo ".worktrees/" >> "$excl"; } 2>/dev/null; ` +
		"mkdir -p '.worktrees' && "
	cmd := preamble + "git worktree add '" + dir + "' -b '" + branch + "' 2>&1"
	if baseRef != nil {
		cmd = preamble + "git worktree add '" + dir + "' -b '" + branch + "' " + shellQuote(*baseRef) + " 2>&1"
	}
	res, err := sb.Shell(ctx, "", gitDiscoveryEnv+cmd, 2*time.Minute)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 && baseRef != nil {
		cmd = preamble + "git worktree add '" + dir + "' -b '" + branch + "' " + shellQuote("origin/"+*baseRef) + " 2>&1"
		if res, err = sb.Shell(ctx, "", gitDiscoveryEnv+cmd, 2*time.Minute); err != nil {
			return "", err
		}
	}
	if res.ExitCode == 0 {
		return dir, nil
	}
	msg := strings.TrimSpace(res.Stdout)
	if msg == "" {
		msg = strings.TrimSpace(res.Stderr)
	}
	if msg == "" {
		msg = "Worktree mode requires a git workspace"
	}
	return "", httpx.BadRequest(msg)
}

// removeWorktree is best effort: dirty trees and unmerged branches are kept.
func removeWorktree(ctx context.Context, sb *sandbox.Sandbox, cwd string) {
	short := filepath.Base(cwd)
	_, _ = sb.Shell(ctx, "", gitDiscoveryEnv+"git worktree remove "+shellQuote(cwd)+" 2>&1 && git branch -d "+shellQuote("worktree-"+short)+" 2>&1", time.Minute)
}
