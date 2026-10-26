package sandboxapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"
)

// binaryExtensions mirrors SANDBOX_BINARY_EXTENSIONS: these files travel as
// base64 and the editor shows them as binary.
var binaryExtensions = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`exe dll so dylib a lib obj o zip tar gz bz2 xz 7z rar jpg jpeg png gif bmp
		ico tiff webp mp4 avi mkv mov wmv flv webm mp3 wav flac ogg wma aac pdf doc docx xls xlsx ppt pptx
		bin dat db sqlite sqlite3 woff woff2 ttf otf eot class jar war ear pyc pyo pyd`) {
		binaryExtensions[e] = true
	}
}

func isBinaryPath(p string) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	return binaryExtensions[ext]
}

// walkSkipDirs keeps the non-git fallback listing out of dependency/build dirs.
var walkSkipDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true,
	".nuxt": true, "dist": true, "build": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true,
	".tox": true, ".eggs": true,
}

type FileMetadata struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	IsBinary *bool  `json:"is_binary"`
}

func boolPtr(b bool) *bool { return &b }

// lsFilesStage matches the "<mode> <object> <stage>\t" prefix `ls-files -s`
// puts on tracked entries (untracked ones are bare paths).
var lsFilesStage = regexp.MustCompile(`^([0-7]{6}) [0-9a-f]{40,64} [0-3]\t`)

// ParseLsFiles turns `git ls-files -s -z` output into entries, synthesizing
// the parent directories git doesn't list. A nested repository (listed as
// "dir/" among untracked files) and a submodule (a 160000 gitlink) are
// directories whose contents aren't listed.
func ParseLsFiles(out string) []FileMetadata {
	items := []FileMetadata{}
	seen := map[string]bool{}
	for _, rel := range strings.Split(out, "\x00") {
		isDir := false
		if m := lsFilesStage.FindStringSubmatch(rel); m != nil {
			rel = rel[len(m[0]):]
			isDir = m[1] == "160000"
		}
		if strings.HasSuffix(rel, "/") {
			rel, isDir = strings.TrimRight(rel, "/"), true
		}
		if rel == "" || seen[rel] {
			continue
		}
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			dir := strings.Join(parts[:i], "/")
			if seen[dir] {
				continue
			}
			seen[dir] = true
			items = append(items, FileMetadata{Path: dir, Type: "directory"})
		}
		seen[rel] = true
		if isDir {
			items = append(items, FileMetadata{Path: rel, Type: "directory"})
		} else {
			items = append(items, FileMetadata{Path: rel, Type: "file", IsBinary: boolPtr(isBinaryPath(rel))})
		}
	}
	return items
}

// listFiles lists files under the workspace-relative dir rel (paths relative
// to rel). git ls-files honors ignore rules; non-git dirs fall back to a walk.
func listFiles(ctx context.Context, sb *sandbox.Sandbox, rel string) ([]FileMetadata, error) {
	repo, err := gitops.OpenHost(sb, rel)
	if err != nil {
		return nil, err
	}
	res, err := repo.Run(ctx, "ls-files", "--cached", "--others", "--exclude-standard", "-s", "-z")
	if err == nil && res.Exit == 0 && res.Stdout != "" {
		return ParseLsFiles(res.Stdout), nil
	}
	root, err := sb.OpenWorkspaceRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	start := "."
	if rel != "" {
		start = rel
	}
	items := []FileMetadata{}
	err = fs.WalkDir(root.FS(), start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == start {
				return err
			}
			return nil
		}
		if p == start {
			return nil
		}
		r := strings.TrimPrefix(p, start+"/")
		if start == "." {
			r = p
		}
		if d.IsDir() {
			if walkSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			items = append(items, FileMetadata{Path: r, Type: "directory"})
			return nil
		}
		items = append(items, FileMetadata{Path: r, Type: "file", IsBinary: boolPtr(isBinaryPath(r))})
		return nil
	})
	return items, err
}

type FileContent struct {
	Content  string `json:"content"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	IsBinary bool   `json:"is_binary"`
	// Lossy: the file isn't valid UTF-8 and Content has replacement
	// characters, so saving it back would corrupt it.
	Lossy bool `json:"lossy"`
	// Version identifies the bytes on disk; a save sends it back as
	// base_version so it can't overwrite a newer version.
	Version string `json:"version"`
}

var (
	errNotAFile = errors.New("not a file")
	errTooLarge = errors.New("file is too large to open")
)

// maxReadBytes caps one file read into memory (the response is base64/JSON
// encoded on top of it).
const maxReadBytes = 25 << 20

// readFile reads a workspace-relative file on the host. The workspace Root is
// anchored at the (non-swappable) mount parent, so it refuses anything —
// including a symlinked project dir or any symlink inside it — that escapes
// the workspace.
func readFile(sb *sandbox.Sandbox, rel string) (FileContent, error) {
	data, err := readRaw(sb, rel)
	if err != nil {
		return FileContent{}, err
	}
	content, binary, lossy := EncodeContent(rel, data)
	return FileContent{Content: content, Path: rel, Type: "file", IsBinary: binary, Lossy: lossy, Version: fileVersion(data)}, nil
}

func readRaw(sb *sandbox.Sandbox, rel string) ([]byte, error) {
	root, err := sb.OpenWorkspaceRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readFrom(root, rel)
}

// readFrom reads a regular file of at most maxReadBytes inside root.
func readFrom(root *os.Root, rel string) ([]byte, error) {
	fi, err := root.Stat(rel)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotAFile
	}
	// The agent controls file sizes: never buffer more than the cap (the file
	// may also grow between the stat and the read).
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReadBytes {
		return nil, errTooLarge
	}
	return data, nil
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func fileVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// EncodeContent base64-encodes binary-extension files and decodes the rest as
// UTF-8, with replacement characters (lossy) when it isn't valid UTF-8.
func EncodeContent(p string, data []byte) (content string, binary, lossy bool) {
	if isBinaryPath(p) {
		return base64.StdEncoding.EncodeToString(data), true, false
	}
	if utf8.Valid(data) {
		return string(data), false, false
	}
	return strings.ToValidUTF8(string(data), "\uFFFD"), false, true
}

// saveFile writes an editor save and returns the new version. A non-empty
// baseVersion must match the file on disk, so a stale buffer can't overwrite
// newer content (an agent's edit, a pull).
func saveFile(sb *sandbox.Sandbox, rel, text, baseVersion string) (string, error) {
	cur, curErr := readRaw(sb, rel)
	if baseVersion != "" && (curErr != nil || fileVersion(cur) != baseVersion) {
		return "", httpx.Conflict("File changed on disk")
	}
	content := []byte(text)
	// The editor drops a UTF-8 BOM; keep the one the file had.
	if curErr == nil && bytes.HasPrefix(cur, utf8BOM) && !bytes.HasPrefix(content, utf8BOM) {
		content = append(append([]byte{}, utf8BOM...), content...)
	}
	if err := writeFile(sb, rel, content); err != nil {
		return "", httpx.BadRequest(err.Error())
	}
	return fileVersion(content), nil
}

// writeFile writes content on the host. In a Docker sandbox run by a root
// API, new files and dirs are handed to the sandbox user so the agent can
// keep editing them.
func writeFile(sb *sandbox.Sandbox, rel string, content []byte) error {
	root, err := sb.OpenWorkspaceRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	chown := sb.Kind == sandbox.KindDocker && os.Geteuid() == 0
	// Create missing parents one level at a time so each new dir can be chowned.
	parts := strings.Split(path.Dir(rel), "/")
	cur := ""
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		cur = path.Join(cur, part)
		fi, err := root.Stat(cur)
		if err == nil {
			if !fi.IsDir() {
				return errors.New("parent path is not a directory: " + cur)
			}
			continue
		}
		if err := root.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if chown {
			_ = root.Lchown(cur, sandbox.ContainerUID, sandbox.ContainerUID)
		}
	}
	perm := fs.FileMode(0o644)
	existed := false
	if fi, err := root.Stat(rel); err == nil {
		if !fi.Mode().IsRegular() {
			return errors.New("not a regular file: " + rel)
		}
		perm = fi.Mode().Perm()
		existed = true
	}
	if err := root.WriteFile(rel, content, perm); err != nil {
		return err
	}
	if chown && !existed {
		_ = root.Lchown(rel, sandbox.ContainerUID, sandbox.ContainerUID)
	}
	return nil
}

// writeZip streams every listed workspace file into a zip archive.
func writeZip(ctx context.Context, sb *sandbox.Sandbox, w io.Writer) error {
	items, err := listFiles(ctx, sb, "")
	if err != nil {
		return err
	}
	root, err := sb.OpenWorkspaceRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	zw := zip.NewWriter(w)
	for _, it := range items {
		if it.Type != "file" {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f, err := root.Open(it.Path)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			f.Close()
			continue
		}
		hdr, _ := zip.FileInfoHeader(fi)
		hdr.Name = filepath.ToSlash(it.Path)
		hdr.Method = zip.Deflate
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			f.Close()
			return err
		}
		_, err = io.Copy(dst, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}
