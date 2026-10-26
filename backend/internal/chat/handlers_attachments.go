package chat

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

// --- attachments ---

var uploadFolders = map[string][2]string{
	"application/pdf": {"pdfs", "pdf"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {"xlsx", "xlsx"},
}

var allowedImages = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

func (m *Module) saveUploads(r *http.Request, userID string) ([]models.Attachment, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File["attached_files"]
	out := make([]models.Attachment, 0, len(files))
	for _, fh := range files {
		a, err := m.saveUpload(fh, userID)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (m *Module) saveUpload(fh *multipart.FileHeader, userID string) (models.Attachment, error) {
	ct := fh.Header.Get("Content-Type")
	var folder, kind string
	switch {
	case allowedImages[ct]:
		folder, kind = "images", "image"
	case uploadFolders[ct] != [2]string{}:
		folder, kind = uploadFolders[ct][0], uploadFolders[ct][1]
	default:
		return models.Attachment{}, httpx.BadRequest("Invalid file type: " + ct)
	}
	if fh.Size > m.a.Cfg.MaxUploadSize {
		return models.Attachment{}, httpx.BadRequest(fmt.Sprintf("File too large: %d bytes exceeds %d bytes", fh.Size, m.a.Cfg.MaxUploadSize))
	}
	src, err := fh.Open()
	if err != nil {
		return models.Attachment{}, err
	}
	defer src.Close()
	rel := filepath.ToSlash(filepath.Join("temp", userID, folder, uuid.NewString()+strings.ToLower(filepath.Ext(fh.Filename))))
	full := filepath.Join(m.a.Cfg.StoragePath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return models.Attachment{}, err
	}
	dst, err := os.Create(full)
	if err != nil {
		return models.Attachment{}, err
	}
	if _, err := io.Copy(dst, io.LimitReader(src, m.a.Cfg.MaxUploadSize+1)); err != nil {
		dst.Close()
		return models.Attachment{}, err
	}
	dst.Close()
	name := fh.Filename
	return models.Attachment{FileURL: m.a.Cfg.APIPrefix + "/attachments/temp/preview?path=" + rel, FilePath: &rel, FileType: kind, Filename: &name}, nil
}

func (m *Module) tempPreview(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	rel := filepath.Clean(filepath.FromSlash(r.URL.Query().Get("path")))
	prefix := filepath.Join("temp", u.ID) + string(filepath.Separator)
	if !strings.HasPrefix(rel, prefix) {
		return httpx.Forbidden("Access denied")
	}
	return m.serveFile(w, r, filepath.Join(m.a.Cfg.StoragePath, rel), filepath.Base(rel), true)
}

func (m *Module) attachmentFile(r *http.Request) (string, string, error) {
	u := user(r)
	id := r.PathValue("attachment_id")
	var rel, owner string
	var name *string
	err := m.a.DB.QueryRowContext(r.Context(), `SELECT a.file_path, a.filename, c.user_id FROM message_attachments a
		JOIN messages msg ON msg.id = a.message_id JOIN chats c ON c.id = msg.chat_id WHERE a.id = ?`, id).Scan(&rel, &name, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", httpx.NotFound("Attachment not found")
	}
	if err != nil {
		return "", "", err
	}
	if owner != u.ID {
		return "", "", httpx.Forbidden("Access denied")
	}
	full := filepath.Clean(filepath.Join(m.a.Cfg.StoragePath, filepath.FromSlash(rel)))
	if !strings.HasPrefix(full, filepath.Clean(m.a.Cfg.StoragePath)+string(filepath.Separator)) {
		return "", "", httpx.Forbidden("Access denied")
	}
	return full, firstNonEmpty(db.Deref(name), filepath.Base(full)), nil
}

func (m *Module) attachmentPreview(w http.ResponseWriter, r *http.Request) error {
	full, name, err := m.attachmentFile(r)
	if err != nil {
		return err
	}
	return m.serveFile(w, r, full, name, true)
}

func (m *Module) attachmentDownload(w http.ResponseWriter, r *http.Request) error {
	full, name, err := m.attachmentFile(r)
	if err != nil {
		return err
	}
	return m.serveFile(w, r, full, name, false)
}

func (m *Module) serveFile(w http.ResponseWriter, r *http.Request, full, name string, inline bool) error {
	f, err := os.Open(full)
	if err != nil {
		return httpx.NotFound("File not found")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return httpx.NotFound("File not found")
	}
	disp := "attachment"
	if inline {
		disp = "inline"
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	ascii := strings.Map(func(r rune) rune {
		if r > 127 || r == '"' {
			return -1
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "file"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disp, ascii, urlPathEscape(name)))
	w.Header().Set("Content-Type", guessMIME(name))
	http.ServeContent(w, r, name, st.ModTime(), f)
	return nil
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
