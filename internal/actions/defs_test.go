package actions

import "testing"

func TestDefsRegisterWithoutConflicts(t *testing.T) {
	r := New()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	if a, ok := r.Lookup(ModeNormal, PagePanes, "n t"); !ok || a.ID != "new.terminal" {
		t.Fatalf("n t → %v %v", a.ID, ok)
	}
	if a, ok := r.Lookup(ModeLive, "", "f4"); !ok || a.ID != "note.new" {
		t.Fatalf("f4 in live → %v %v", a.ID, ok)
	}
}

func TestConflictsRejected(t *testing.T) {
	r := NewDefault()
	for _, a := range []Action{
		{ID: "x1", Verb: "X", Keys: []string{"x"}, Page: PagePanes},
		{ID: "x2", Verb: "X", Keys: []string{"n"}},                               // prefix of "n t"
		{ID: "x3", Verb: "X", Keys: []string{"J k"}},                             // extends "J"
		{ID: "x4", Verb: "X", Keys: []string{"f4"}, Modes: []string{ModeInsert}}, // global
	} {
		if err := r.CheckConflicts(a); err == nil {
			t.Fatalf("%s: expected conflict", a.ID)
		}
	}
	if err := r.CheckConflicts(Action{ID: "ok", Verb: "X", Keys: []string{"x"}, Page: PageHosts}); err != nil {
		t.Fatalf("other page should not conflict: %v", err)
	}
}
