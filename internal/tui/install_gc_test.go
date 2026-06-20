package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// The superseded plugin cache dir from a deferred UpdateInstall must survive until the
// registry is actually persisted. Deleting it earlier is what stranded Claude Code with
// a "plugin cache does not exist" error when an update was applied but not saved.
func TestPendingCacheGCSurvivesDiscard(t *testing.T) {
	st, _ := buildState(t)
	oldDir := filepath.Join(t.TempDir(), "old-version")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate an in-memory update that queued the old dir for GC but was never applied.
	st.pendingCacheGC = append(st.pendingCacheGC, oldDir)
	st.dirtyPlugins = true

	// Discard == never calling save(). The dir must still be present so the on-disk
	// registry (which still references it) stays valid.
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatal("queued-but-unsaved cache dir must not be deleted on discard")
	}
}

// Once the registry saves successfully, the queued cache dir is GC'd and the queue clears.
func TestPendingCacheGCRunsAfterSave(t *testing.T) {
	st, _ := buildState(t)
	oldDir := filepath.Join(t.TempDir(), "old-version")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st.pendingCacheGC = append(st.pendingCacheGC, oldDir)
	st.dirtyPlugins = true

	if _, err := st.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(oldDir); err == nil {
		t.Error("after a successful save the superseded cache dir should be GC'd")
	}
	if len(st.pendingCacheGC) != 0 {
		t.Errorf("pendingCacheGC should be cleared after a successful save, got %v", st.pendingCacheGC)
	}
}
