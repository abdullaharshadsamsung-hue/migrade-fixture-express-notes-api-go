// Package httpapi exposes the notes JSON CRUD API over HTTP. It replaces the
// Express application defined in the source project's index.js.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"migrated-app/internal/notes"
)

// maxBodyBytes mirrors express.json()'s default "100kb" limit.
const maxBodyBytes = 100 * 1024

// Repository is the storage contract required by the HTTP handlers. It is
// satisfied by *notes.Store.
type Repository interface {
	List(ctx context.Context) []notes.Note
	Get(ctx context.Context, id int64) (notes.Note, bool)
	Create(ctx context.Context, in notes.CreateInput) notes.Note
	Update(ctx context.Context, id int64, p notes.Patch) (notes.Note, bool)
	Delete(ctx context.Context, id int64) bool
}

// RegisterRoutes registers every notes API route on r, backed by a fresh
// process-local in-memory store (the source module held its state the same
// way, as a module-level singleton).
func RegisterRoutes(r chi.Router) {
	Mount(r, notes.NewStore())
}

// Mount registers every notes API route on r using repo for storage. It is
// exported so tests can inject their own Repository.
func Mount(r chi.Router, repo Repository) {
	h := &handlers{repo: repo}

	// Express routing is non-strict, so a single trailing slash is tolerated;
	// Express also answers HEAD for every GET route. Both are reproduced here.
	// MIGRATION_NOTE: Express routing is also case-insensitive (/NOTES works).
	// That cannot be expressed per-route in chi; if required, add a
	// path-lowercasing middleware at the router level in cmd/server.
	get := func(path string, fn http.HandlerFunc) {
		for _, p := range withTrailingSlash(path) {
			r.Get(p, fn)
			r.Head(p, fn)
		}
	}
	each := func(method, path string, fn http.HandlerFunc) {
		for _, p := range withTrailingSlash(path) {
			r.Method(method, p, fn)
		}
	}

	get("/health", h.health)
	get("/notes", h.listNotes)
	get("/notes/{id}", h.getNote)
	each(http.MethodPost, "/notes", h.createNote)
	each(http.MethodPut, "/notes/{id}", h.updateNote)
	each(http.MethodDelete, "/notes/{id}", h.deleteNote)
}

func withTrailingSlash(path string) []string {
	return []string{path, path + "/"}
}

type handlers struct {
	repo Repository
}

type errorBody struct {
	Error string `json:"error"`
}

var errNotFound = errorBody{Error: "not found"}

// health is the liveness probe; it always reports ok.
func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// listNotes returns every stored note (an empty array when there are none).
func (h *handlers) listNotes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.repo.List(r.Context()))
}

// getNote returns one note by id, or 404 for a missing/unparseable id.
func (h *handlers) getNote(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errNotFound)
		return
	}
	note, found := h.repo.Get(r.Context(), id)
	if !found {
		writeJSON(w, http.StatusNotFound, errNotFound)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

// createNote creates a note from {title, body}; title must be a non-empty
// JSON string, body may be any JSON value (absent defaults to "").
func (h *handlers) createNote(w http.ResponseWriter, r *http.Request) {
	fields, perr := readBody(r)
	if perr != nil {
		writeJSON(w, perr.status, errorBody{Error: perr.msg})
		return
	}

	title, ok := stringField(fields, "title")
	if !ok || title == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "title is required"})
		return
	}

	in := notes.CreateInput{Title: title}
	if raw, present := fields["body"]; present {
		in.Body = raw // explicit null is preserved as null
	}
	writeJSON(w, http.StatusCreated, h.repo.Create(r.Context(), in))
}

// updateNote partially updates a note's title and/or body. Title validation
// deliberately happens before the existence check, as in the source.
func (h *handlers) updateNote(w http.ResponseWriter, r *http.Request) {
	fields, perr := readBody(r)
	if perr != nil {
		writeJSON(w, perr.status, errorBody{Error: perr.msg})
		return
	}

	var patch notes.Patch
	if _, present := fields["title"]; present {
		title, ok := stringField(fields, "title")
		if !ok || title == "" {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "title must be a non-empty string"})
			return
		}
		patch.Title = &title
	}
	if raw, present := fields["body"]; present {
		patch.Body = raw // body:null is applied and sets body to null
	}

	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errNotFound)
		return
	}
	note, found := h.repo.Update(r.Context(), id, patch)
	if !found {
		writeJSON(w, http.StatusNotFound, errNotFound)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

// deleteNote removes a note by id and answers 204 with no body.
func (h *handlers) deleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok || !h.repo.Delete(r.Context(), id) {
		writeJSON(w, http.StatusNotFound, errNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stringField returns fields[key] decoded as a string, and false when the key
// is absent or the value is not a JSON string (mirrors typeof === "string").
func stringField(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// ---------------------------------------------------------------------------
// Body parsing (reproduces express.json() defaults)
// ---------------------------------------------------------------------------

type parseError struct {
	status int
	msg    string
}

// readBody mimics express.json() followed by `req.body || {}` and object
// destructuring: it returns the top-level fields of a JSON object body.
//   - no body, or a Content-Type other than application/json → empty fields
//   - body larger than 100kb → 413
//   - non-utf charset → 415
//   - top-level primitive (strict mode) or malformed JSON → 400
//   - JSON array → empty fields (destructuring an array yields undefined)
//
// MIGRATION_NOTE: Express answered body-parser failures with an HTML error
// page; here they are JSON {"error": "..."} bodies with the same status.
func readBody(r *http.Request) (map[string]json.RawMessage, *parseError) {
	empty := map[string]json.RawMessage{}

	if r.Body == nil || r.Body == http.NoBody {
		return empty, nil
	}

	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return empty, nil
	}
	if cs := strings.ToLower(params["charset"]); cs != "" && !strings.HasPrefix(cs, "utf-") {
		return nil, &parseError{http.StatusUnsupportedMediaType, `unsupported charset "` + strings.ToUpper(cs) + `"`}
	}

	if r.ContentLength > maxBodyBytes {
		return nil, &parseError{http.StatusRequestEntityTooLarge, "request entity too large"}
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, &parseError{http.StatusRequestEntityTooLarge, "request entity too large"}
		}
		return nil, &parseError{http.StatusBadRequest, "request aborted"}
	}
	if len(data) > maxBodyBytes {
		return nil, &parseError{http.StatusRequestEntityTooLarge, "request entity too large"}
	}
	if len(data) == 0 {
		return empty, nil
	}

	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, &parseError{http.StatusBadRequest, "Unexpected end of JSON input"}
	}
	switch trimmed[0] {
	case '{':
		if !json.Valid(data) {
			return nil, &parseError{http.StatusBadRequest, "invalid JSON body"}
		}
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, &parseError{http.StatusBadRequest, "invalid JSON body"}
		}
		return fields, nil
	case '[':
		if !json.Valid(data) {
			return nil, &parseError{http.StatusBadRequest, "invalid JSON body"}
		}
		return empty, nil
	default:
		return nil, &parseError{http.StatusBadRequest, "invalid JSON body: top-level value must be an object or array"}
	}
}

// ---------------------------------------------------------------------------
// Id coercion (reproduces JavaScript Number())
// ---------------------------------------------------------------------------

var decimalNumber = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// maxSafeInt is Number.MAX_SAFE_INTEGER; beyond it JS ids lose precision.
const maxSafeInt = 1<<53 - 1

// parseID converts a path segment the way JavaScript's Number() would and
// reports whether the result is an integer that could address a note.
// "12" → 12, "1.0"/"1e0"/"0x1"/"0b1"/"0o1" → 1, "" or whitespace → 0.
// NaN, Infinity and non-integral values report false (→ 404, never 400).
func parseID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true // Number("") === 0
	}

	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			v, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil || v > maxSafeInt {
				return 0, false
			}
			return int64(v), true
		}
	}

	if !decimalNumber.MatchString(s) {
		return 0, false // NaN or ±Infinity: never matches a note
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	if f != math.Trunc(f) || math.Abs(f) > maxSafeInt {
		return 0, false
	}
	return int64(f), true
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// writeJSON writes v as JSON like Express's res.json: charset=utf-8 content
// type and no HTML escaping.
func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
