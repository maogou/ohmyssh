package service

import (
	"fmt"

	"github.com/maogou/ohmyssh/internal/repository"
)

// CredentialService manages the passwords ohmyssh remembers.
//
// It returns what it did rather than printing it: the command layer owns the
// wording, and the store stays the only thing that knows about files.
type CredentialService interface {
	// Forget deletes the password saved for target, resolving that target the
	// same way a connection would. ok is false when nothing was saved for it.
	Forget(configPath, target string) (key string, ok bool, err error)
	// ForgetAll deletes every saved password, returning how many there were.
	ForgetAll() (int, error)
	// Saved lists the identities with a saved password, sorted.
	Saved() []string
	// Path reports where the passwords are stored, for help text.
	Path() string
}

// NewCredentialService wires the credential use cases to their dependencies.
//
// Unlike ConnectService there is no logger to carry here: nothing on this path
// has anything to say beyond what it returns.
func NewCredentialService(hosts repository.Hosts, creds repository.Credential) CredentialService {
	return &credentialService{hosts: hosts, creds: creds}
}

type credentialService struct {
	hosts repository.Hosts
	creds repository.Credential
}

func (s *credentialService) Forget(configPath, target string) (string, bool, error) {
	all, err := s.hosts.List(configPath)
	if err != nil {
		return "", false, fmt.Errorf("read ssh config: %w", err)
	}
	host, err := s.hosts.Find(configPath, all, target)
	if err != nil {
		return "", false, err
	}

	key := s.creds.Key(host)
	removed, err := s.creds.Delete(key)
	if err != nil {
		return "", false, err
	}
	return key, removed, nil
}

func (s *credentialService) ForgetAll() (int, error) { return s.creds.Clear() }

func (s *credentialService) Saved() []string { return s.creds.Keys() }

func (s *credentialService) Path() string { return s.creds.Path() }
