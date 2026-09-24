// Package credential remembers host passwords so that connecting does not mean
// typing one every time.
//
// Passwords are kept in credentials.json in ~/.ohmyssh, alongside anything else
// ohmyssh keeps for this user — %USERPROFILE%\.ohmyssh on Windows, which is what
// os.UserHomeDir reports there. The file is encrypted with AES-256-GCM under a
// key derived from the local user, machine and OS. Be clear about what that
// buys: it keeps the passwords out of sight of anything that merely reads the
// file — a backup, a sync client, a stray `cat` — but the key is derivable by
// anyone who can already run code as this user, so it is not a defence against a
// compromised account. The file is written 0600 and the directory 0700, which
// Windows applies as the read-only attribute and nothing else: there the
// encryption is the whole of the protection.
//
// A store copied to another machine will not decrypt; the passwords are simply
// not found and the caller falls back to prompting.
package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/appdir"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// keySeed separates this store's key from any other derived from the same
// machine identity.
const keySeed = "ohmyssh-secret-key-"

// record is one stored password. Password holds base64 AES-256-GCM ciphertext.
type record struct {
	Password  string    `json:"password"`
	UpdatedAt time.Time `json:"updated_at"`
}

// mu serialises access to the store file. Reads and writes are small and
// infrequent, so the file is re-read per operation rather than cached: a cached
// copy would go stale if two ohmyssh processes ran at once.
var mu sync.Mutex

// Key names the credential a host needs.
//
// It is the login identity rather than the config alias, so two aliases for the
// same machine share one saved password, and editing User or Port in the config
// asks for the new password instead of quietly reusing the old one.
func Key(host config.SSHHost) string {
	return host.DisplayUser() + "@" + host.Addr()
}

// Path reports where the store lives, for help text and messages.
func Path() string {
	p, err := path()
	if err != nil {
		return "credentials.json"
	}
	return p
}

// dir is the directory ohmyssh keeps this user's files in, which is where the
// hosts added from the browser live too.
func dir() (string, error) {
	return appdir.Dir()
}

func path() (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "credentials.json"), nil
}

// legacyPath is where the store lived before ohmyssh kept its files in
// ~/.ohmyssh. It is read once, to carry a store written by an older version
// across, and never written.
func legacyPath() (string, error) {
	d := os.Getenv("XDG_CONFIG_HOME")
	if d == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate home directory: %w", err)
		}
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "ohmyssh", "credentials.json"), nil
}

// Get returns the saved password for key.
func Get(key string) (string, bool) {
	if key == "" {
		return "", false
	}

	mu.Lock()
	defer mu.Unlock()

	records, err := load()
	if err != nil {
		if !os.IsNotExist(err) {
			zlog.L().Debug().Err(err).Msg("could not read saved passwords")
		}
		return "", false
	}

	rec, ok := records[key]
	if !ok || rec.Password == "" {
		return "", false
	}

	password, err := decrypt(rec.Password, deriveKey())
	if err != nil {
		// Almost always a store from a different machine or user account.
		zlog.L().Debug().Err(err).Str("key", key).Msg("saved password did not decrypt; ignoring it")
		return "", false
	}
	return password, true
}

// Set saves password for key, replacing any previous value.
func Set(key, password string) error {
	if key == "" {
		return errors.New("credential key cannot be empty")
	}
	if password == "" {
		_, err := Delete(key)
		return err
	}

	mu.Lock()
	defer mu.Unlock()

	records, err := load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if records == nil {
		records = make(map[string]record)
	}

	encrypted, err := encrypt(password, deriveKey())
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}
	records[key] = record{Password: encrypted, UpdatedAt: time.Now().UTC()}
	return save(records)
}

// Delete removes the saved password for key, reporting whether one was there.
func Delete(key string) (bool, error) {
	if key == "" {
		return false, nil
	}

	mu.Lock()
	defer mu.Unlock()

	records, err := load()
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if _, ok := records[key]; !ok {
		return false, nil
	}

	delete(records, key)
	return true, save(records)
}

// Clear removes every saved password, reporting how many there were.
func Clear() (int, error) {
	mu.Lock()
	defer mu.Unlock()

	records, err := load()
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}

	removed := len(records)
	return removed, save(make(map[string]record))
}

// Keys lists the identities that have a saved password, sorted.
func Keys() []string {
	mu.Lock()
	defer mu.Unlock()

	records, err := load()
	if err != nil {
		return nil
	}

	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// load reads the store, returning an error satisfying os.IsNotExist when there
// is nothing saved yet.
func load() (map[string]record, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		// Nothing has been saved here yet, which is the state a store written by
		// an older version leaves behind. Its passwords cannot be re-derived —
		// the key is bound to this machine and account — so one found at the old
		// location is brought across rather than abandoned.
		data, err = adoptLegacy(p)
	}
	if err != nil {
		return nil, err
	}

	records := make(map[string]record)
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return records, nil
}

// adoptLegacy carries a store from where it lived before ~/.ohmyssh into p and
// returns its contents. A machine that never had one gets the legacy path's own
// error, which satisfies os.IsNotExist and reads to every caller as "nothing
// saved yet".
//
// It is called from load rather than once at startup, so it is a no-op after the
// first write and retries by itself if it could not finish the first time.
func adoptLegacy(p string) ([]byte, error) {
	from, err := legacyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return nil, err
	}

	// A move that fails is not a read that failed. The passwords are in hand, so
	// they are returned and the move is tried again on the next load rather than
	// being reported as a store the user has lost.
	if err := move(from, p, data); err != nil {
		zlog.L().Warn().Err(err).Str("from", from).Str("to", p).
			Msg("could not move saved passwords into ~/.ohmyssh; still reading them where they are")
	} else {
		zlog.L().Info().Str("from", from).Str("to", p).Msg("moved saved passwords into ~/.ohmyssh")
	}
	return data, nil
}

// move writes data to p and only then removes from, so that a failure at any
// point leaves the store readable in at least one place.
func move(from, p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := replaceStore(p, data); err != nil {
		return err
	}
	if err := os.Remove(from); err != nil {
		return fmt.Errorf("remove %s: %w", from, err)
	}
	return nil
}

// save writes the store, creating the directory 0700 and the file 0600.
func save(records map[string]record) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return replaceStore(p, data)
}

// replaceStore replaces path with data.
//
// The bytes land in a temporary file beside it and are renamed into place, so a
// crash, a full disk or an interrupted write cannot leave a truncated store
// behind. That matters more here than it does for a hosts file: a damaged store
// cannot be repaired by hand, because the saved passwords exist nowhere else and
// the half that was written is what survives.
//
// CreateTemp opens the file 0600, so the store lands with the mode it needs
// without a separate chmod — including when it replaces a file that was left
// more permissive.
func replaceStore(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create a temporary file beside %s: %w", path, err)
	}
	defer func() {
		// Once the rename has happened there is nothing to remove, and before it
		// there is a file here the user never asked for.
		_ = os.Remove(tmp.Name())
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	// Flush before the rename. Otherwise the rename can land while the contents
	// are still only in the page cache, and a power loss then leaves an empty
	// file where a readable store used to be — the one outcome worse than a
	// store that is merely out of date.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// deriveKey builds the encryption key from the local machine and user identity.
func deriveKey() []byte {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	hostname, _ := os.Hostname()

	sum := sha256.Sum256([]byte(keySeed + user + "@" + hostname + "-" + runtime.GOOS))
	return sum[:]
}

// encrypt seals plaintext with AES-256-GCM, prefixing the nonce.
func encrypt(plaintext string, key []byte) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

// decrypt opens base64 ciphertext produced by encrypt.
func decrypt(ciphertext string, key []byte) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", errors.New("saved password is truncated")
	}

	plaintext, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
