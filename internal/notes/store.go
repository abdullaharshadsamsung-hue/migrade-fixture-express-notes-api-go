// Package notes provides a process-local, concurrency-safe in-memory store
// for notes. It replaces the CommonJS notes.js module from the source project.
package notes

import (
	"context"
	"encoding/json"
	"sync"
)

// defaultBody is the JSON encoding of the empty string, used when a note is
// created without a body (mirrors the JS default parameter `body = ""`).
var defaultBody = json.RawMessage(`""`)

// Note is a single stored note. Body is kept as raw JSON because the source
// API accepts any JSON value for body (string, null, object, ...).
type Note struct {
	ID    int64           `json:"id"`
	Title string          `json:"title"`
	Body  json.RawMessage `json:"body"`
}

// CreateInput holds the fields for a new note. A nil Body means "absent"
// and defaults to the JSON string "". An explicit JSON null must be passed
// as json.RawMessage("null") and is preserved as-is.
type CreateInput struct {
	Title string
	Body  json.RawMessage
}

// Patch describes a shallow merge into an existing note. Nil fields leave
// the corresponding value unchanged; the id can never be patched.
type Patch struct {
	Title *string
	Body  json.RawMessage
}

// Store is an in-memory note store. The zero value is not usable; create
// one with NewStore. All methods are safe for concurrent use and return
// copies, never references to internal state.
type Store struct {
	mu     sync.RWMutex
	notes  map[int64]*Note
	order  []int64
	nextID int64
}

// NewStore returns an empty Store whose first assigned id is 1.
func NewStore() *Store {
	return &Store{
		notes:  make(map[int64]*Note),
		order:  make([]int64, 0),
		nextID: 1,
	}
}

// List returns all stored notes in insertion order. Updates do not change
// a note's position. The result is never nil.
func (s *Store) List(_ context.Context) []Note {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Note, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, cloneNote(s.notes[id]))
	}
	return out
}

// Get returns the note with the given id and true, or a zero Note and false
// if no such note exists.
func (s *Store) Get(_ context.Context, id int64) (Note, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, ok := s.notes[id]
	if !ok {
		return Note{}, false
	}
	return cloneNote(n), true
}

// Create stores a new note with the next auto-incremented id and returns it.
func (s *Store) Create(_ context.Context, in CreateInput) Note {
	body := in.Body
	if body == nil {
		body = defaultBody
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	n := &Note{
		ID:    s.nextID,
		Title: in.Title,
		Body:  cloneRaw(body),
	}
	s.nextID++
	s.notes[n.ID] = n
	s.order = append(s.order, n.ID)
	return cloneNote(n)
}

// Update shallow-merges p into the note with the given id and returns the
// updated note and true. It returns a zero Note and false if not found.
func (s *Store) Update(_ context.Context, id int64, p Patch) (Note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.notes[id]
	if !ok {
		return Note{}, false
	}
	if p.Title != nil {
		n.Title = *p.Title
	}
	if p.Body != nil {
		n.Body = cloneRaw(p.Body)
	}
	return cloneNote(n), true
}

// Delete removes the note with the given id and reports whether a note was
// deleted. Ids are never reused after deletion.
func (s *Store) Delete(_ context.Context, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.notes[id]; !ok {
		return false
	}
	delete(s.notes, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return true
}

func cloneNote(n *Note) Note {
	return Note{ID: n.ID, Title: n.Title, Body: cloneRaw(n.Body)}
}

func cloneRaw(r json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	out := make(json.RawMessage, len(r))
	copy(out, r)
	return out
}
