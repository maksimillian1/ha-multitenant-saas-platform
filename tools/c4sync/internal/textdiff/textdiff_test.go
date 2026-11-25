package textdiff

import "testing"

func TestUnified(t *testing.T) {
	if Unified("a", "b", "x\n", "x\n", 2) != "" {
		t.Fatal("equal texts must give no diff")
	}
	got := Unified("old", "new", "a\nb\nc\nd\n", "a\nb\nC\nd\ne\n", 1)
	want := "--- old\n+++ new\n@@ -2,3 +2,4 @@\n b\n-c\n+C\n d\n+e\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if got := Unified("old", "new", "", "a\n", 2); got != "--- old\n+++ new\n@@ -1,0 +1,1 @@\n+a\n" {
		t.Fatalf("from empty: %q", got)
	}
}
