package credential

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/config"
)

// isolate gives the store a home of its own, so a test neither reads nor writes
// the saved passwords of whoever is running it. It returns the path the store is
// expected to be written to and the path an older version would have used; the
// legacy one exists only if the test puts a file there.
func isolate(t *testing.T) (store, legacy string) {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "config")
	for _, dir := range []string{home, config} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	// os.UserHomeDir reads HOME everywhere but Windows, where it reads
	// USERPROFILE; both are set so the store lands in the temporary home on any
	// system the tests are run on.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", config)

	return filepath.Join(home, ".ohmyssh", "credentials.json"),
		filepath.Join(config, "ohmyssh", "credentials.json")
}

// writeStore puts a store holding password at path, encrypted the way a
// running ohmyssh would have written it — by the same machine, so it can be read
// back, which is what makes it worth carrying across. The record is stored under
// the login these tests look passwords up by.
func writeStore(t *testing.T, path, password string) {
	t.Helper()

	sealed, err := encrypt(password, deriveKey())
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	data, err := json.Marshal(map[string]record{"root@example.com:22": {Password: sealed}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestSetThenGetRoundTrips(t *testing.T) {
	isolate(t)

	if err := Set("root@example.com:22", "hunter2"); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, ok := Get("root@example.com:22")
	if !ok {
		t.Fatal("Get reported no saved password after Set")
	}
	if got != "hunter2" {
		t.Errorf("password = %q, want %q", got, "hunter2")
	}
}

func TestGetMissingKeyIsNotFound(t *testing.T) {
	isolate(t)

	if _, ok := Get("nobody@example.com:22"); ok {
		t.Error("Get found a password that was never stored")
	}
}

// A store is written to disk, not kept only in memory.
func TestPasswordSurvivesAReload(t *testing.T) {
	path, _ := isolate(t)
	if err := Set("root@example.com:22", "s3cret"); err != nil {
		t.Fatalf("set: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if strings.Contains(string(data), "s3cret") {
		t.Error("the password appears in plaintext in the store file")
	}

	// A fresh process reads the same file: nothing is cached in memory.
	got, ok := Get("root@example.com:22")
	if !ok || got != "s3cret" {
		t.Errorf("after reload: password = %q, ok = %v; want %q, true", got, ok, "s3cret")
	}
}

func TestStoreIsPrivateAndParsable(t *testing.T) {
	path, _ := isolate(t)
	if err := Set("root@example.com:22", "s3cret"); err != nil {
		t.Fatalf("set: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store mode = %o, want 600", perm)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat store dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("store directory mode = %o, want 700", perm)
	}

	// The file stays readable JSON so it can be inspected and repaired by hand.
	var records map[string]record
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("store is not valid JSON: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("store holds %d records, want 1", len(records))
	}
}

// A store written before ohmyssh kept its files in ~/.ohmyssh is carried across,
// passwords and all: the key is bound to this machine and account, so the
// ciphertext stays readable after the move, and it is not readable at all if the
// file is simply left behind.
func TestStoreFromTheOldLocationIsCarriedAcross(t *testing.T) {
	store, legacy := isolate(t)
	writeStore(t, legacy, "hunter2")

	got, ok := Get("root@example.com:22")
	if !ok || got != "hunter2" {
		t.Fatalf("after the move: password = %q, ok = %v; want %q, true", got, ok, "hunter2")
	}

	if _, err := os.Stat(store); err != nil {
		t.Errorf("the store was not written to %s: %v", store, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("the old store is still at %s, so there are now two: %v", legacy, err)
	}
}

// The move happens once. Once a store is in the new location it is the only one
// read, so a file that reappears at the old one — a restored backup, an older
// build on the same machine — cannot roll the passwords back.
func TestAnExistingStoreIsNotReplacedByTheOldOne(t *testing.T) {
	store, legacy := isolate(t)
	writeStore(t, legacy, "stale")

	if err := Set("root@example.com:22", "current"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// The Set above read through the old file and moved it; put it back, as an
	// older build writing to the location it knows would.
	writeStore(t, legacy, "stale")

	got, ok := Get("root@example.com:22")
	if !ok || got != "current" {
		t.Errorf("password = %q, ok = %v; want %q, true", got, ok, "current")
	}
	if _, err := os.Stat(store); err != nil {
		t.Errorf("stat store: %v", err)
	}
	// And nothing deletes it in passing: the file is not ours to lose, and it may
	// be the only copy for another install on this machine.
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("the old store was removed even though it was not read: %v", err)
	}
}

// A machine that never had a store must read as "nothing saved yet" — the state
// every caller already handles — rather than as the legacy path's own error.
func TestNoStoreAnywhereIsSimplyNotFound(t *testing.T) {
	isolate(t)

	if _, ok := Get("root@example.com:22"); ok {
		t.Error("Get found a password with no store anywhere")
	}
	if keys := Keys(); keys != nil {
		t.Errorf("Keys() = %v with no store anywhere, want nil", keys)
	}
	if removed, err := Delete("root@example.com:22"); err != nil || removed {
		t.Errorf("Delete = (%v, %v), want (false, nil)", removed, err)
	}
}

// If the store cannot be moved, the passwords are still read from where they
// are rather than reported as lost — the move is an optimisation, and the user's
// saved passwords must not depend on it succeeding.
func TestAFailedMoveStillReadsTheOldStore(t *testing.T) {
	_, legacy := isolate(t)
	writeStore(t, legacy, "hunter2")

	// A file where the new store's directory needs to be: there is no system on
	// which a path can be created underneath one, so the move cannot go through.
	blocked := blockedParent(t)

	data, err := adoptLegacy(filepath.Join(blocked, "credentials.json"))
	if err != nil {
		t.Fatalf("adoptLegacy reported a failed move as a failed read: %v", err)
	}
	if !strings.Contains(string(data), "root@example.com:22") {
		t.Errorf("adoptLegacy returned %q, which does not hold the saved password", data)
	}
	if contents, err := os.ReadFile(legacy); err != nil || string(contents) != string(data) {
		t.Errorf("the old store did not survive a move that failed: %q, %v", contents, err)
	}
}

// The move copies before it deletes, so the store is never briefly in neither
// place — which is the one ordering that would lose it.
func TestAFailedMoveLeavesTheOldStoreIntact(t *testing.T) {
	_, legacy := isolate(t)
	writeStore(t, legacy, "hunter2")
	before, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}

	dest := filepath.Join(blockedParent(t), "credentials.json")
	if err := move(legacy, dest, []byte("{}")); err == nil {
		t.Fatal("move reported success writing underneath a file")
	}

	after, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("the old store is gone after a move that failed: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the old store was rewritten by a move that failed:\n%s", after)
	}
}

// blockedParent returns the path of a file, for use where a directory is needed.
func blockedParent(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("in the way"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	return file
}

// Permissions are re-tightened even when the file already exists loosely.
func TestSaveTightensLoosePermissions(t *testing.T) {
	path, _ := isolate(t)
	if err := Set("root@example.com:22", "one"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := Set("root@example.com:22", "two"); err != nil {
		t.Fatalf("set again: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store mode = %o after rewrite, want 600", perm)
	}
}

// Nothing partial is left beside the store. A temporary file that outlived the
// write would be a second copy of every saved password, and it would be picked
// up by anything that copies the directory — a backup, a sync, a dotfiles
// repository — without ever being removed by ohmyssh itself.
func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	store, _ := isolate(t)
	for i, password := range []string{"one", "two"} {
		if err := Set("root@example.com:22", password); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(store))
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	var found []string
	for _, e := range entries {
		found = append(found, e.Name())
	}
	if len(found) != 1 || found[0] != filepath.Base(store) {
		t.Errorf("store directory holds %v, want just %s", found, filepath.Base(store))
	}
}

// A write that fails leaves no temporary file, and the store it could not
// replace is still the store: the rename is the only step that changes what is
// on disk, so a failure before it changes nothing at all.
func TestFailedStoreWriteLeavesNothingBehind(t *testing.T) {
	store, _ := isolate(t)
	if err := Set("root@example.com:22", "hunter2"); err != nil {
		t.Fatalf("set: %v", err)
	}
	before, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}

	// A directory in the store's place fails the rename, and it fails after the
	// data has already been written to the temporary file — the state a crash
	// partway through the old write left behind.
	dir := filepath.Dir(store)
	if err := os.Remove(store); err != nil {
		t.Fatalf("remove store: %v", err)
	}
	if err := os.Mkdir(store, 0o700); err != nil {
		t.Fatalf("mkdir where the store was: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store, "occupy"), []byte("x"), 0o600); err != nil {
		t.Fatalf("occupy the directory: %v", err)
	}
	if err := replaceStore(store, before); err == nil {
		t.Fatal("replacing the store with a directory in its place reported success")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	var found []string
	for _, e := range entries {
		found = append(found, e.Name())
	}
	if len(found) != 1 || found[0] != filepath.Base(store) {
		t.Errorf("store directory holds %v, want just %s", found, filepath.Base(store))
	}
}

func TestSetReplacesPreviousPassword(t *testing.T) {
	isolate(t)

	if err := Set("root@example.com:22", "old"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := Set("root@example.com:22", "new"); err != nil {
		t.Fatalf("set again: %v", err)
	}

	got, ok := Get("root@example.com:22")
	if !ok || got != "new" {
		t.Errorf("password = %q, ok = %v; want %q, true", got, ok, "new")
	}
	if keys := Keys(); len(keys) != 1 {
		t.Errorf("Keys() = %v, want one entry", keys)
	}
}

func TestDeleteReportsWhetherSomethingWasThere(t *testing.T) {
	isolate(t)

	if removed, err := Delete("root@example.com:22"); err != nil || removed {
		t.Errorf("Delete on an empty store = (%v, %v), want (false, nil)", removed, err)
	}

	if err := Set("root@example.com:22", "pw"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if removed, err := Delete("root@example.com:22"); err != nil || !removed {
		t.Errorf("Delete = (%v, %v), want (true, nil)", removed, err)
	}
	if _, ok := Get("root@example.com:22"); ok {
		t.Error("password is still readable after Delete")
	}
}

func TestClearRemovesEverything(t *testing.T) {
	isolate(t)

	for _, key := range []string{"a@x:22", "b@y:22"} {
		if err := Set(key, "pw"); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	removed, err := Clear()
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if removed != 2 {
		t.Errorf("Clear() = %d, want 2", removed)
	}
	if keys := Keys(); len(keys) != 0 {
		t.Errorf("Keys() = %v after Clear, want none", keys)
	}
}

// Set with an empty password is a delete, so clearing one entry never leaves a
// record holding nothing.
func TestSetEmptyPasswordDeletes(t *testing.T) {
	isolate(t)

	if err := Set("root@example.com:22", "pw"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := Set("root@example.com:22", ""); err != nil {
		t.Fatalf("set empty: %v", err)
	}
	if _, ok := Get("root@example.com:22"); ok {
		t.Error("empty Set left the password readable")
	}
}

// A store written by a different machine or user cannot be decrypted, and must
// read as "no password" rather than as an error the user cannot act on.
func TestForeignStoreIsIgnored(t *testing.T) {
	path, _ := isolate(t)

	foreignKey := sha256.Sum256([]byte("a key from some other machine and user"))
	foreign, err := encrypt("elsewhere", foreignKey[:])
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	data, _ := json.Marshal(map[string]record{"root@example.com:22": {Password: foreign}})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, ok := Get("root@example.com:22"); ok {
		t.Error("Get returned a password it could not have decrypted")
	}
}

func TestCorruptStoreDoesNotPanic(t *testing.T) {
	path, _ := isolate(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, ok := Get("root@example.com:22"); ok {
		t.Error("Get returned a password from a corrupt store")
	}
	if keys := Keys(); keys != nil {
		t.Errorf("Keys() = %v from a corrupt store, want nil", keys)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := deriveKey()

	ciphertext, err := encrypt("a password with spaces and 日本語", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plaintext != "a password with spaces and 日本語" {
		t.Errorf("round trip = %q", plaintext)
	}

	// The same plaintext must not encrypt to the same bytes twice.
	again, err := encrypt("a password with spaces and 日本語", key)
	if err != nil {
		t.Fatalf("encrypt again: %v", err)
	}
	if again == ciphertext {
		t.Error("two encryptions produced identical ciphertext; the nonce is not random")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key := deriveKey()
	ciphertext, err := encrypt("original", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Flip a character in the body; GCM must refuse it.
	tampered := []byte(ciphertext)
	tampered[len(tampered)-4] ^= 'A' ^ 'B'
	if _, err := decrypt(string(tampered), key); err == nil {
		t.Error("decrypt accepted tampered ciphertext")
	}
}

func TestKeyIdentifiesTheLoginNotTheAlias(t *testing.T) {
	// Two aliases for the same login on the same machine are one credential.
	first := config.SSHHost{Name: "web1", Hostname: "10.0.0.1", User: "deploy", Port: "22"}
	second := config.SSHHost{Name: "frontend", Hostname: "10.0.0.1", User: "deploy", Port: "22"}
	if Key(first) != Key(second) {
		t.Errorf("aliases for one login got different keys: %q vs %q", Key(first), Key(second))
	}

	// Changing user, host or port is a different credential.
	for _, other := range []config.SSHHost{
		{Name: "web1", Hostname: "10.0.0.1", User: "root", Port: "22"},
		{Name: "web1", Hostname: "10.0.0.2", User: "deploy", Port: "22"},
		{Name: "web1", Hostname: "10.0.0.1", User: "deploy", Port: "2222"},
	} {
		if Key(first) == Key(other) {
			t.Errorf("Key(%+v) collides with Key(%+v)", other, first)
		}
	}

	// An omitted port means 22, so it matches an explicit 22.
	implicit := config.SSHHost{Name: "web1", Hostname: "10.0.0.1", User: "deploy"}
	if Key(implicit) != Key(first) {
		t.Errorf("default port did not match explicit 22: %q vs %q", Key(implicit), Key(first))
	}

	if got, want := Key(first), "deploy@10.0.0.1:22"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
}
