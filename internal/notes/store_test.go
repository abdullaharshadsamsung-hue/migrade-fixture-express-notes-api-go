package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestCreate(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		in       CreateInput
		wantBody string
	}{
		{"no body defaults to empty string", CreateInput{Title: "first"}, `""`},
		{"string body", CreateInput{Title: "t", Body: json.RawMessage(`"hello"`)}, `"hello"`},
		{"explicit null preserved", CreateInput{Title: "t", Body: json.RawMessage(`null`)}, `null`},
		{"object body", CreateInput{Title: "t", Body: json.RawMessage(`{"a":1}`)}, `{"a":1}`},
		{"empty title allowed", CreateInput{}, `""`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			n := s.Create(ctx, tc.in)
			if n.ID != 1 {
				t.Fatalf("id = %d, want 1", n.ID)
			}
			if n.Title != tc.in.Title {
				t.Fatalf("title = %q, want %q", n.Title, tc.in.Title)
			}
			if string(n.Body) != tc.wantBody {
				t.Fatalf("body = %s, want %s", n.Body, tc.wantBody)
			}
			got, ok := s.Get(ctx, n.ID)
			if !ok || got.Title != tc.in.Title || string(got.Body) != tc.wantBody {
				t.Fatalf("Get = %+v, %v", got, ok)
			}
		})
	}
}

func TestCreateIDsSequentialAndNotReused(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	a := s.Create(ctx, CreateInput{Title: "a"})
	b := s.Create(ctx, CreateInput{Title: "b"})
	if b.ID != a.ID+1 {
		t.Fatalf("second id %d, want %d", b.ID, a.ID+1)
	}
	if !s.Delete(ctx, b.ID) {
		t.Fatal("delete failed")
	}
	c := s.Create(ctx, CreateInput{Title: "c"})
	if c.ID != b.ID+1 {
		t.Fatalf("id reused or skipped: got %d want %d", c.ID, b.ID+1)
	}
	if len(s.List(ctx)) != 2 {
		t.Fatal("expected 2 notes stored")
	}
}

func TestNoteJSONFields(t *testing.T) {
	s := NewStore()
	n := s.Create(context.Background(), CreateInput{Title: "x"})
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("fields = %v, want exactly id,title,body", m)
	}
	for _, k := range []string{"id", "title", "body"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing field %q", k)
		}
	}
	if string(m["body"]) != `""` {
		t.Fatalf("body = %s", m["body"])
	}
}

func TestCreateCopiesInput(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	body := json.RawMessage(`"abc"`)
	n := s.Create(ctx, CreateInput{Title: "t", Body: body})
	body[1] = 'X'
	n.Body[1] = 'Y'
	got, _ := s.Get(ctx, n.ID)
	if string(got.Body) != `"abc"` {
		t.Fatalf("store mutated via input/output: %s", got.Body)
	}
	if string(defaultBody) != `""` {
		t.Fatal("defaultBody mutated")
	}
	d := s.Create(ctx, CreateInput{})
	d.Body[0] = 'Z'
	if string(defaultBody) != `""` {
		t.Fatal("defaultBody aliased")
	}
}

func TestGet(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	first := s.Create(ctx, CreateInput{Title: "first"})
	tests := []struct {
		name      string
		id        int64
		wantOK    bool
		wantTitle string
	}{
		{"existing", first.ID, true, "first"},
		{"missing", 9999, false, ""},
		{"zero", 0, false, ""},
		{"negative", -1, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := s.Get(ctx, tc.id)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				if got.ID != 0 || got.Title != "" || got.Body != nil {
					t.Fatalf("expected zero Note, got %+v", got)
				}
				return
			}
			if got.Title != tc.wantTitle {
				t.Fatalf("title = %q", got.Title)
			}
		})
	}
	if len(s.List(ctx)) != 1 {
		t.Fatal("Get had side effects")
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		create    []string
		remove    []int64
		wantTitle []string
	}{
		{"empty", nil, nil, []string{}},
		{"insertion order", []string{"a", "b", "c"}, nil, []string{"a", "b", "c"}},
		{"some removed", []string{"a", "b", "c", "d"}, []int64{2, 4}, []string{"a", "c"}},
		{"all removed", []string{"a"}, []int64{1}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			for _, title := range tc.create {
				s.Create(ctx, CreateInput{Title: title})
			}
			for _, id := range tc.remove {
				s.Delete(ctx, id)
			}
			got := s.List(ctx)
			if got == nil {
				t.Fatal("List returned nil")
			}
			if len(got) != len(tc.wantTitle) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.wantTitle))
			}
			for i, w := range tc.wantTitle {
				if got[i].Title != w {
					t.Fatalf("[%d] = %q, want %q", i, got[i].Title, w)
				}
			}
		})
	}
}

func TestListOrderUnchangedByUpdate(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	s.Create(ctx, CreateInput{Title: "a"})
	s.Create(ctx, CreateInput{Title: "b"})
	s.Update(ctx, 1, Patch{Title: strPtr("a2")})
	got := s.List(ctx)
	if got[0].Title != "a2" || got[1].Title != "b" {
		t.Fatalf("order/content wrong: %+v", got)
	}
}

func TestListReturnsCopy(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	s.Create(ctx, CreateInput{Title: "a", Body: json.RawMessage(`"x"`)})
	l := s.List(ctx)
	l[0].Title = "mutated"
	l[0].Body[1] = 'Q'
	_ = append(l, Note{ID: 99})
	again := s.List(ctx)
	if len(again) != 1 || again[0].Title != "a" || string(again[0].Body) != `"x"` {
		t.Fatalf("store affected by mutation: %+v", again)
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		id        int64
		patch     Patch
		wantOK    bool
		wantTitle string
		wantBody  string
	}{
		{"title only", 1, Patch{Title: strPtr("new")}, true, "new", `"orig"`},
		{"body only", 1, Patch{Body: json.RawMessage(`"nb"`)}, true, "old", `"nb"`},
		{"both", 1, Patch{Title: strPtr("n"), Body: json.RawMessage(`{"k":true}`)}, true, "n", `{"k":true}`},
		{"empty patch", 1, Patch{}, true, "old", `"orig"`},
		{"title to empty", 1, Patch{Title: strPtr("")}, true, "", `"orig"`},
		{"body to null", 1, Patch{Body: json.RawMessage(`null`)}, true, "old", `null`},
		{"nonexistent", 9999, Patch{Title: strPtr("x")}, false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			s.Create(ctx, CreateInput{Title: "old", Body: json.RawMessage(`"orig"`)})
			got, ok := s.Update(ctx, tc.id, tc.patch)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v", ok)
			}
			if !ok {
				if got.ID != 0 || got.Title != "" || got.Body != nil {
					t.Fatalf("expected zero Note, got %+v", got)
				}
				l := s.List(ctx)
				if len(l) != 1 || l[0].Title != "old" {
					t.Fatalf("store changed: %+v", l)
				}
				return
			}
			if got.ID != tc.id || got.Title != tc.wantTitle || string(got.Body) != tc.wantBody {
				t.Fatalf("got %+v", got)
			}
			stored, _ := s.Get(ctx, tc.id)
			if stored.ID != tc.id || stored.Title != tc.wantTitle || string(stored.Body) != tc.wantBody {
				t.Fatalf("stored %+v", stored)
			}
		})
	}
}

func TestUpdateCopiesPatchAndResult(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	s.Create(ctx, CreateInput{Title: "a"})
	b := json.RawMessage(`"pp"`)
	got, _ := s.Update(ctx, 1, Patch{Body: b})
	b[1] = 'Z'
	got.Body[1] = 'Y'
	stored, _ := s.Get(ctx, 1)
	if !bytes.Equal(stored.Body, []byte(`"pp"`)) {
		t.Fatalf("aliasing: %s", stored.Body)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		id   int64
		want bool
	}{
		{"nonexistent 9999", 9999, false},
		{"existing", 1, true},
		{"zero", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			s.Create(ctx, CreateInput{Title: "a"})
			if got := s.Delete(ctx, tc.id); got != tc.want {
				t.Fatalf("Delete = %v, want %v", got, tc.want)
			}
			_, ok := s.Get(ctx, 1)
			if tc.want && ok {
				t.Fatal("note still present")
			}
			if !tc.want && !ok {
				t.Fatal("note wrongly removed")
			}
		})
	}
}

func TestDeleteTwice(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	n := s.Create(ctx, CreateInput{Title: "a"})
	if !s.Delete(ctx, n.ID) {
		t.Fatal("first delete should be true")
	}
	if s.Delete(ctx, n.ID) {
		t.Fatal("second delete should be false")
	}
	if len(s.List(ctx)) != 0 {
		t.Fatal("list not empty")
	}
}

func TestConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := s.Create(ctx, CreateInput{Title: "x"})
			s.Get(ctx, c.ID)
			s.Update(ctx, c.ID, Patch{Title: strPtr("y")})
			s.List(ctx)
		}()
	}
	wg.Wait()
	l := s.List(ctx)
	if len(l) != n {
		t.Fatalf("len = %d", len(l))
	}
	seen := map[int64]bool{}
	for _, note := range l {
		if note.ID < 1 || note.ID > n || seen[note.ID] {
			t.Fatalf("bad id %d", note.ID)
		}
		seen[note.ID] = true
	}
}