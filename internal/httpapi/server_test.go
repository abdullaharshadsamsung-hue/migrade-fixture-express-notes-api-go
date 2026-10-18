package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"migrated-app/internal/notes"
)

func newTestServer(t *testing.T) (http.Handler, *notes.Store) {
	t.Helper()
	store := notes.NewStore()
	r := chi.NewRouter()
	Mount(r, store)
	return r, store
}

func do(t *testing.T, h http.Handler, method, path, body, ctype string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const jsonCT = "application/json"

func decodeNote(t *testing.T, b []byte) notes.Note {
	t.Helper()
	var n notes.Note
	if err := json.Unmarshal(b, &n); err != nil {
		t.Fatalf("decode note %q: %v", b, err)
	}
	return n
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
	var e map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body not JSON: %s", rec.Body.String())
	}
	s, ok := e["error"].(string)
	if !ok {
		t.Fatalf("error field not a string: %s", rec.Body.String())
	}
	if msg != "" && s != msg {
		t.Errorf("error = %q, want %q", s, msg)
	}
}

func seed(t *testing.T, s *notes.Store, title, body string) notes.Note {
	t.Helper()
	in := notes.CreateInput{Title: title}
	if body != "" {
		in.Body = json.RawMessage(body)
	}
	return s.Create(context.Background(), in)
}

func TestHealth(t *testing.T) {
	h, store := newTestServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/health"},
		{http.MethodGet, "/health/"},
		{http.MethodHead, "/health"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, "", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if tc.method == http.MethodGet && rec.Body.String() != `{"status":"ok"}` {
				t.Errorf("body %q", rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("ct %q", ct)
			}
		})
	}
	if len(store.List(context.Background())) != 0 {
		t.Error("health modified state")
	}
}

func TestListNotes(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		h, _ := newTestServer(t)
		rec := do(t, h, http.MethodGet, "/notes", "", "")
		if rec.Code != 200 || rec.Body.String() != "[]" {
			t.Fatalf("got %d %q", rec.Code, rec.Body.String())
		}
	})
	t.Run("reflects create update delete", func(t *testing.T) {
		h, _ := newTestServer(t)
		do(t, h, http.MethodPost, "/notes", `{"title":"a","body":"x"}`, jsonCT)
		do(t, h, http.MethodPost, "/notes", `{"title":"b"}`, jsonCT)
		do(t, h, http.MethodPost, "/notes", `{"title":"c"}`, jsonCT)
		do(t, h, http.MethodPut, "/notes/1", `{"title":"A"}`, jsonCT)
		do(t, h, http.MethodDelete, "/notes/2", "", "")
		rec := do(t, h, http.MethodGet, "/notes/", "", "")
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		var list []notes.Note
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 || list[0].ID != 1 || list[0].Title != "A" || string(list[0].Body) != `"x"` || list[1].ID != 3 {
			t.Fatalf("list = %+v", list)
		}
		// read-only
		rec2 := do(t, h, http.MethodGet, "/notes", "", "")
		if rec2.Body.String() != rec.Body.String() {
			t.Error("list not idempotent")
		}
	})
}

func TestGetNote(t *testing.T) {
	h, store := newTestServer(t)
	n := seed(t, store, "t", `"b"`)
	deleted := seed(t, store, "d", "")
	store.Delete(context.Background(), deleted.ID)

	tests := []struct {
		name   string
		path   string
		status int
	}{
		{"existing", "/notes/1", 200},
		{"trailing slash", "/notes/1/", 200},
		{"float form", "/notes/1.0", 200},
		{"exponent", "/notes/1e0", 200},
		{"hex", "/notes/0x1", 200},
		{"nonexistent", "/notes/999", 404},
		{"deleted", "/notes/2", 404},
		{"non-numeric", "/notes/abc", 404},
		{"fractional", "/notes/1.5", 404},
		{"infinity", "/notes/Infinity", 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tc.path, "", "")
			if tc.status == 404 {
				assertError(t, rec, 404, "not found")
				return
			}
			if rec.Code != 200 {
				t.Fatalf("status %d", rec.Code)
			}
			got := decodeNote(t, rec.Body.Bytes())
			if got.ID != n.ID || got.Title != "t" || string(got.Body) != `"b"` {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestCreateNote(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		ctype    string
		status   int
		errMsg   string
		wantBody string
	}{
		{"title and body", `{"title":"hi","body":"there"}`, jsonCT, 201, "", `"there"`},
		{"title only defaults body", `{"title":"hi"}`, jsonCT, 201, "", `""`},
		{"null body preserved", `{"title":"hi","body":null}`, jsonCT, 201, "", `null`},
		{"object body", `{"title":"hi","body":{"a":1}}`, jsonCT, 201, "", `{"a":1}`},
		{"extra fields ignored", `{"title":"hi","id":99,"x":1}`, jsonCT, 201, "", `""`},
		{"charset utf-8", `{"title":"hi"}`, "application/json; charset=utf-8", 201, "", `""`},
		{"missing title", `{"body":"x"}`, jsonCT, 400, "title is required", ""},
		{"empty title", `{"title":""}`, jsonCT, 400, "title is required", ""},
		{"number title", `{"title":5}`, jsonCT, 400, "title is required", ""},
		{"bool title", `{"title":true}`, jsonCT, 400, "title is required", ""},
		{"object title", `{"title":{}}`, jsonCT, 400, "title is required", ""},
		{"null title", `{"title":null}`, jsonCT, 400, "title is required", ""},
		{"no body", "", "", 400, "title is required", ""},
		{"non-json content type", `{"title":"hi"}`, "text/plain", 400, "title is required", ""},
		{"array body", `[1,2]`, jsonCT, 400, "title is required", ""},
		{"malformed json", `{"title":`, jsonCT, 400, "", ""},
		{"top-level primitive", `"hi"`, jsonCT, 400, "", ""},
		{"whitespace only", `   `, jsonCT, 400, "", ""},
		{"bad charset", `{"title":"hi"}`, "application/json; charset=latin1", 415, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newTestServer(t)
			rec := do(t, h, http.MethodPost, "/notes", tc.body, tc.ctype)
			if tc.status != 201 {
				assertError(t, rec, tc.status, tc.errMsg)
				if len(store.List(context.Background())) != 0 {
					t.Error("note created on error")
				}
				return
			}
			if rec.Code != 201 {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			n := decodeNote(t, rec.Body.Bytes())
			if n.ID != 1 || n.Title != "hi" || string(n.Body) != tc.wantBody {
				t.Errorf("got %+v", n)
			}
			g := do(t, h, http.MethodGet, "/notes/1", "", "")
			if g.Code != 200 {
				t.Errorf("not visible via GET: %d", g.Code)
			}
		})
	}
}

func TestCreateNoteTooLarge(t *testing.T) {
	h, _ := newTestServer(t)
	big := `{"title":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	rec := do(t, h, http.MethodPost, "/notes", big, jsonCT)
	assertError(t, rec, http.StatusRequestEntityTooLarge, "request entity too large")
}

func TestCreateNoteUniqueIDs(t *testing.T) {
	h, _ := newTestServer(t)
	seen := map[int64]bool{}
	for i := 0; i < 5; i++ {
		rec := do(t, h, http.MethodPost, "/notes/", `{"title":"x"}`, jsonCT)
		n := decodeNote(t, rec.Body.Bytes())
		if seen[n.ID] {
			t.Fatalf("duplicate id %d", n.ID)
		}
		seen[n.ID] = true
	}
}

func TestUpdateNote(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		body      string
		status    int
		errMsg    string
		wantTitle string
		wantBody  string
	}{
		{"title only", "/notes/1", `{"title":"new"}`, 200, "", "new", `"orig"`},
		{"body only", "/notes/1", `{"body":"nb"}`, 200, "", "t", `"nb"`},
		{"both", "/notes/1/", `{"title":"n","body":"b"}`, 200, "", "n", `"b"`},
		{"body null applied", "/notes/1", `{"body":null}`, 200, "", "t", `null`},
		{"empty object unchanged", "/notes/1", `{}`, 200, "", "t", `"orig"`},
		{"no body unchanged", "/notes/1", "", 200, "", "t", `"orig"`},
		{"id ignored", "/notes/1", `{"id":42,"title":"x"}`, 200, "", "x", `"orig"`},
		{"empty title", "/notes/1", `{"title":""}`, 400, "title must be a non-empty string", "", ""},
		{"null title", "/notes/1", `{"title":null}`, 400, "title must be a non-empty string", "", ""},
		{"number title", "/notes/1", `{"title":3}`, 400, "title must be a non-empty string", "", ""},
		{"validation before existence", "/notes/999", `{"title":""}`, 400, "title must be a non-empty string", "", ""},
		{"validation before bad id", "/notes/abc", `{"title":1}`, 400, "title must be a non-empty string", "", ""},
		{"nonexistent", "/notes/999", `{"title":"x"}`, 404, "not found", "", ""},
		{"non-numeric", "/notes/abc", `{"title":"x"}`, 404, "not found", "", ""},
		{"malformed", "/notes/1", `{bad`, 400, "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newTestServer(t)
			seed(t, store, "t", `"orig"`)
			ct := jsonCT
			if tc.body == "" {
				ct = ""
			}
			rec := do(t, h, http.MethodPut, tc.path, tc.body, ct)
			if tc.status != 200 {
				assertError(t, rec, tc.status, tc.errMsg)
				n, _ := store.Get(context.Background(), 1)
				if n.Title != "t" || string(n.Body) != `"orig"` {
					t.Errorf("note mutated: %+v", n)
				}
				return
			}
			if rec.Code != 200 {
				t.Fatalf("status %d %s", rec.Code, rec.Body.String())
			}
			n := decodeNote(t, rec.Body.Bytes())
			if n.ID != 1 || n.Title != tc.wantTitle || string(n.Body) != tc.wantBody {
				t.Errorf("got %+v", n)
			}
			stored, _ := store.Get(context.Background(), 1)
			if stored.Title != tc.wantTitle || !bytes.Equal(stored.Body, []byte(tc.wantBody)) {
				t.Errorf("stored %+v", stored)
			}
		})
	}
}

func TestDeleteNote(t *testing.T) {
	h, store := newTestServer(t)
	seed(t, store, "a", "")
	seed(t, store, "b", "")

	rec := do(t, h, http.MethodDelete, "/notes/1", "", "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	assertError(t, do(t, h, http.MethodGet, "/notes/1", "", ""), 404, "not found")
	var list []notes.Note
	_ = json.Unmarshal(do(t, h, http.MethodGet, "/notes", "", "").Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != 2 {
		t.Errorf("list %+v", list)
	}

	for _, tc := range []struct{ name, path string }{
		{"already deleted", "/notes/1"},
		{"nonexistent", "/notes/999"},
		{"non-numeric", "/notes/abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertError(t, do(t, h, http.MethodDelete, tc.path, "", ""), 404, "not found")
		})
	}
	if rec := do(t, h, http.MethodDelete, "/notes/2/", "", ""); rec.Code != 204 {
		t.Errorf("trailing slash delete %d", rec.Code)
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"12", 12, true}, {"", 0, true}, {"  ", 0, true}, {" 7 ", 7, true},
		{"1.0", 1, true}, {"1e0", 1, true}, {"0x1", 1, true}, {"0b11", 3, true},
		{"0o7", 7, true}, {"-3", -3, true}, {"+4", 4, true}, {".5", 0, false},
		{"abc", 0, false}, {"1.5", 0, false}, {"Infinity", 0, false},
		{"NaN", 0, false}, {"0xZZ", 0, false}, {"9007199254740993", 0, false},
		{"1e400", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseID(tc.in)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Errorf("parseID(%q) = %d,%v want %d,%v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestWriteJSONNoHTMLEscape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]string{"a": "<b>&"})
	if rec.Body.String() != `{"a":"<b>&"}` {
		t.Errorf("body %q", rec.Body.String())
	}
}

type fakeRepo struct {
	notes.Store
	lastPatch notes.Patch
	called    string
}

func (f *fakeRepo) List(context.Context) []notes.Note { f.called = "list"; return []notes.Note{} }
func (f *fakeRepo) Get(context.Context, int64) (notes.Note, bool) {
	f.called = "get"
	return notes.Note{}, false
}
func (f *fakeRepo) Create(_ context.Context, in notes.CreateInput) notes.Note {
	f.called = "create"
	return notes.Note{ID: 7, Title: in.Title, Body: in.Body}
}
func (f *fakeRepo) Update(_ context.Context, id int64, p notes.Patch) (notes.Note, bool) {
	f.called = "update"
	f.lastPatch = p
	return notes.Note{}, false
}
func (f *fakeRepo) Delete(context.Context, int64) bool { f.called = "delete"; return false }

func TestMountWithMockRepository(t *testing.T) {
	repo := &fakeRepo{}
	r := chi.NewRouter()
	Mount(r, repo)

	rec := do(t, r, http.MethodPost, "/notes", `{"title":"x"}`, jsonCT)
	if rec.Code != 201 || repo.called != "create" {
		t.Fatalf("create: %d %s", rec.Code, repo.called)
	}
	if rec.Body.String() != `{"id":7,"title":"x","body":null}` {
		t.Errorf("body %q", rec.Body.String())
	}

	rec = do(t, r, http.MethodPut, "/notes/3", `{"title":"y"}`, jsonCT)
	assertError(t, rec, 404, "not found")
	if repo.lastPatch.Title == nil || *repo.lastPatch.Title != "y" || repo.lastPatch.Body != nil {
		t.Errorf("patch %+v", repo.lastPatch)
	}
}

func TestRegisterRoutes(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutes(r)
	if rec := do(t, r, http.MethodGet, "/notes", "", ""); rec.Code != 200 || rec.Body.String() != "[]" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}