// Package repository wraps ohmyssh's low-level machinery — the SSH transport,
// the ssh config parser and the credential store — behind interfaces.
//
// The interfaces are here so the service layer can be exercised without an SSH
// server, a real config file or a writable credential store. Transport is the
// one that earns its keep: the password policy lives in service, and that
// policy can only be tested if dialling can be made to fail on demand.
package repository
