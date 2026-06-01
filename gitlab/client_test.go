package gitlab

import (
	"errors"
	"testing"
)

func newTestClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	cfg.Token = "test-token"
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNew_RequiresToken(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestResolveProject_DefaultAndExplicit(t *testing.T) {
	c := newTestClient(t, Config{Project: "grp/app"})

	got, err := c.resolveProject("")
	if err != nil || got != "grp/app" {
		t.Fatalf("default project: got %q err %v", got, err)
	}
	got, err = c.resolveProject("other/repo")
	if err != nil || got != "other/repo" {
		t.Fatalf("explicit project: got %q err %v", got, err)
	}
}

func TestResolveProject_NoneBound(t *testing.T) {
	c := newTestClient(t, Config{})
	if _, err := c.resolveProject(""); err == nil {
		t.Fatal("expected error when no project specified and none bound")
	}
}

func TestAllowList_Enforced(t *testing.T) {
	c := newTestClient(t, Config{Projects: []string{"grp/app", "grp/infra"}})

	if _, err := c.resolveProject("grp/app"); err != nil {
		t.Errorf("allowed project rejected: %v", err)
	}
	// Case-insensitive match.
	if _, err := c.resolveProject("GRP/Infra"); err != nil {
		t.Errorf("case-insensitive allowed project rejected: %v", err)
	}
	_, err := c.resolveProject("grp/secret")
	if !errors.Is(err, ErrProjectNotAllowed) {
		t.Errorf("expected ErrProjectNotAllowed, got %v", err)
	}
}

func TestAllowList_EmptyAllowsAll(t *testing.T) {
	c := newTestClient(t, Config{})
	if _, err := c.resolveProject("any/project"); err != nil {
		t.Errorf("empty allow-list should permit any project, got %v", err)
	}
}

func TestRequireWrite_ReadOnly(t *testing.T) {
	ro := newTestClient(t, Config{ReadOnly: true})
	if !ro.ReadOnly() {
		t.Fatal("expected ReadOnly() true")
	}
	if err := ro.requireWrite(); !errors.Is(err, ErrReadOnly) {
		t.Errorf("expected ErrReadOnly, got %v", err)
	}

	rw := newTestClient(t, Config{})
	if err := rw.requireWrite(); err != nil {
		t.Errorf("read-write client should permit writes, got %v", err)
	}
}

func TestWriteMethods_BlockedWhenReadOnly(t *testing.T) {
	c := newTestClient(t, Config{Project: "grp/app", ReadOnly: true})

	if _, err := c.CreateMergeRequest("", "t", "", "feat", "main"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("CreateMergeRequest: expected ErrReadOnly, got %v", err)
	}
	if _, err := c.UpdateMergeRequest("", 1, "t", "", ""); !errors.Is(err, ErrReadOnly) {
		t.Errorf("UpdateMergeRequest: expected ErrReadOnly, got %v", err)
	}
	if _, err := c.AddMergeRequestNote("", 1, "hi"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("AddMergeRequestNote: expected ErrReadOnly, got %v", err)
	}
	if _, err := c.AddIssueNote("", 1, "hi"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("AddIssueNote: expected ErrReadOnly, got %v", err)
	}
}

func TestBaseURL_Default(t *testing.T) {
	c := newTestClient(t, Config{})
	if c.BaseURL() != "https://gitlab.com" {
		t.Errorf("expected default base URL, got %q", c.BaseURL())
	}
	c2 := newTestClient(t, Config{BaseURL: "https://gl.example.com"})
	if c2.BaseURL() != "https://gl.example.com" {
		t.Errorf("expected custom base URL, got %q", c2.BaseURL())
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" a, b ,, c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
