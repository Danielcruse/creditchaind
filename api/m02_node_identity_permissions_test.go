package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoadNodeIdentity_WritesFileWithOwnerOnlyPermissions is the
// regression test for audit finding M-02: loadNodeIdentity used to
// write data/identity.json - which embeds the raw Ed25519 PRIVATE key,
// not just the public one - with mode 0644, readable by every local
// account on Unix-like deployments. A fresh identity file must now be
// created 0600 (owner read/write only).
//
// File permission bits are a Unix concept - Go's os.WriteFile mode
// argument only toggles the read-only attribute on Windows, so this
// assertion is only meaningful on the platforms this code actually
// gets deployed to (Linux Lightsail nodes).
func TestLoadNodeIdentity_WritesFileWithOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permission bits don't apply on Windows")
	}

	t.Setenv("DATA_DIR", t.TempDir())

	loadNodeIdentity()

	info, err := os.Stat(dataPath("identity.json"))
	if err != nil {
		t.Fatalf("expected identity.json to exist after loadNodeIdentity, got: %v", err)
	}

	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("expected identity.json to be created with mode 0600 (owner-only), got %o", got)
	}
}

// TestLoadNodeIdentity_TightensPermissionsOnAnExistingLooseFile covers
// the upgrade path: a node running before this fix already has an
// identity.json sitting on disk with the old 0644 mode. The very next
// load must tighten it to 0600 without requiring a manual chmod step
// or losing the existing keypair.
func TestLoadNodeIdentity_TightensPermissionsOnAnExistingLooseFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permission bits don't apply on Windows")
	}

	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	// Simulate a pre-fix identity.json: valid content, loose permissions.
	stored := storedIdentity{PublicKey: "aa", PrivateKey: "bb"}
	out, _ := json.MarshalIndent(stored, "", "  ")
	path := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatalf("test setup: %v", err)
	}

	loadNodeIdentity()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected identity.json to still exist: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("expected loadNodeIdentity to tighten an existing 0644 identity.json to 0600, got %o", got)
	}
}
