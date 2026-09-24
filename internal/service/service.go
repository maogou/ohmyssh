// Package service holds what ohmyssh actually does: choosing which host to
// connect to, which password to try, and when to remember one.
//
// It has no dependency on the CLI framework. That is what makes the password
// policy — the part with the most branches and the least obvious behaviour —
// testable by calling it directly instead of driving the command line.
package service

import "github.com/rs/zerolog"

// Service carries what the services in this package share. Embedding it keeps
// the logger out of each constructor's parameter list beyond this one.
type Service struct {
	logger *zerolog.Logger
}

// NewService returns the shared base.
func NewService(logger *zerolog.Logger) *Service {
	return &Service{logger: logger}
}
