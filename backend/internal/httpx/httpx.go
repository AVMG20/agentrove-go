// Package httpx holds HTTP plumbing shared by every handler: JSON IO, errors in
// the FastAPI `{"detail": ...}` shape the frontend expects, SSE, middleware.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Error is an HTTP error with a status and a user-facing detail.
type Error struct {
	Status int
	Detail any
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %v", e.Status, e.Detail) }

func Errorf(status int, format string, args ...any) *Error {
	return &Error{Status: status, Detail: fmt.Sprintf(format, args...)}
}

func BadRequest(msg string) *Error   { return &Error{Status: http.StatusBadRequest, Detail: msg} }
func NotFound(msg string) *Error     { return &Error{Status: http.StatusNotFound, Detail: msg} }
func Forbidden(msg string) *Error    { return &Error{Status: http.StatusForbidden, Detail: msg} }
func Conflict(msg string) *Error     { return &Error{Status: http.StatusConflict, Detail: msg} }
func Unauthorized(msg string) *Error { return &Error{Status: http.StatusUnauthorized, Detail: msg} }
func Unprocessable(msg string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Detail: msg}
}

// HandlerFunc returns an error instead of writing one; Wrap renders it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

func Wrap(h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var he *Error
	if errors.As(err, &he) {
		JSON(w, he.Status, map[string]any{"detail": he.Detail})
		return
	}
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	JSON(w, http.StatusInternalServerError, map[string]any{"detail": "Internal server error"})
}

func JSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(emptySlices(v))
}

func OK(w http.ResponseWriter, v any) error { return JSON(w, http.StatusOK, v) }

func Created(w http.ResponseWriter, v any) error { return JSON(w, http.StatusCreated, v) }

func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// Decode reads a JSON body into v (max 10MB).
func Decode(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, 10<<20)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return Unprocessable("Request body is required")
		}
		return Unprocessable("Invalid JSON body: " + err.Error())
	}
	return nil
}

// ParseForm parses multipart or urlencoded forms.
func ParseForm(r *http.Request, maxMemory int64) error {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxMemory); err != nil {
			return Unprocessable("Invalid multipart form: " + err.Error())
		}
		return nil
	}
	if err := r.ParseForm(); err != nil {
		return Unprocessable("Invalid form: " + err.Error())
	}
	return nil
}

func FormBool(r *http.Request, key string, def bool) bool {
	v := r.FormValue(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.ToLower(v))
	if err != nil {
		return def
	}
	return b
}

func FormOptional(r *http.Request, key string) *string {
	if r.Form == nil && r.MultipartForm == nil {
		return nil
	}
	if _, ok := r.Form[key]; !ok {
		if r.MultipartForm == nil {
			return nil
		}
		if _, ok := r.MultipartForm.Value[key]; !ok {
			return nil
		}
	}
	v := r.FormValue(key)
	if v == "" {
		return nil
	}
	return &v
}

func QueryInt(r *http.Request, key string, def, min, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

func QueryBool(r *http.Request, key string) *bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(strings.ToLower(v))
	if err != nil {
		return nil
	}
	return &b
}

// Recover turns panics into 500s instead of killing the connection.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic", "err", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
				JSON(w, http.StatusInternalServerError, map[string]any{"detail": "Internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if sw.status >= 500 {
			slog.Warn("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
		} else {
			slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
		}
	})
}

func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	all := false
	for _, o := range origins {
		if o == "*" {
			all = true
		}
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (all || allowed[origin]) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Add("Vary", "Origin")
				if r.Method == http.MethodOptions {
					h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					reqHeaders := r.Header.Get("Access-Control-Request-Headers")
					if reqHeaders == "" {
						reqHeaders = "Authorization, Content-Type"
					}
					h.Set("Access-Control-Allow-Headers", reqHeaders)
					h.Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ClientIP is the address rate limits and refresh tokens are keyed on. The
// direct peer is used unless it is one of trusted (our reverse proxies);
// then X-Forwarded-For is walked from the right, skipping trusted hops, since
// only the entries appended by our own proxies can't be forged by the client.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(peer.Unmap(), trusted) {
		return host
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				hops = append(hops, part)
			}
		}
	}
	if len(hops) == 0 {
		return host
	}
	for i := len(hops) - 1; i >= 0; i-- {
		addr, ok := parseHop(hops[i])
		if !ok {
			return hops[i]
		}
		if !isTrusted(addr, trusted) {
			return addr.String()
		}
	}
	// Every hop is one of ours (an internal caller): the furthest one.
	addr, _ := parseHop(hops[0])
	return addr.String()
}

// parseHop accepts "ip", "ip:port" and "[v6]:port" entries.
func parseHop(s string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
