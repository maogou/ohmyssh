package repository

import (
	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/credential"
)

// Credential is the store of remembered host passwords.
type Credential interface {
	// Key names the credential a host needs. It is the login identity rather
	// than the config alias, so two aliases for one machine share an entry.
	Key(host config.SSHHost) string
	Get(key string) (string, bool)
	Set(key, password string) error
	Delete(key string) (bool, error)
	Clear() (int, error)
	Keys() []string
	Path() string
}

// NewCredential returns the encrypted-file-backed store.
func NewCredential() Credential { return credentialStore{} }

type credentialStore struct{}

func (credentialStore) Key(host config.SSHHost) string  { return credential.Key(host) }
func (credentialStore) Get(key string) (string, bool)   { return credential.Get(key) }
func (credentialStore) Set(key, password string) error  { return credential.Set(key, password) }
func (credentialStore) Delete(key string) (bool, error) { return credential.Delete(key) }
func (credentialStore) Clear() (int, error)             { return credential.Clear() }
func (credentialStore) Keys() []string                  { return credential.Keys() }
func (credentialStore) Path() string                    { return credential.Path() }
