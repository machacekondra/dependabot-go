package dependabot

import "fmt"

// The errors below mirror the subset of Dependabot::* exceptions that the
// updater classifies and reports back to the service. Each carries the detail
// the API needs, so callers can type-assert rather than match on message text.

// FileNotFoundError reports a required manifest or lockfile that is missing.
type FileNotFoundError struct {
	FilePath string
	Detail   string
}

func (e *FileNotFoundError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s not found: %s", e.FilePath, e.Detail)
	}
	return fmt.Sprintf("%s not found", e.FilePath)
}

// FileNotParseableError reports a file Dependabot could fetch but not read.
type FileNotParseableError struct {
	FilePath string
	Detail   string
}

func (e *FileNotParseableError) Error() string {
	return fmt.Sprintf("%s is not parseable: %s", e.FilePath, e.Detail)
}

// FileNotResolvableError reports a dependency set that cannot be resolved.
type FileNotResolvableError struct {
	Detail string
}

func (e *FileNotResolvableError) Error() string {
	return fmt.Sprintf("dependencies could not be resolved: %s", e.Detail)
}

// PrivateSourceAuthFailureError reports a registry that rejected our
// credentials, or that we have none for.
type PrivateSourceAuthFailureError struct {
	Source string
}

func (e *PrivateSourceAuthFailureError) Error() string {
	return fmt.Sprintf("the following source could not be reached as it requires authentication: %s", e.Source)
}

// PrivateSourceTimedOutError reports a registry that did not respond in time.
type PrivateSourceTimedOutError struct {
	Source string
}

func (e *PrivateSourceTimedOutError) Error() string {
	return fmt.Sprintf("the following source timed out: %s", e.Source)
}

// PrivateSourceBadResponseError reports a registry that responded but not
// usefully.
type PrivateSourceBadResponseError struct {
	Source string
}

func (e *PrivateSourceBadResponseError) Error() string {
	return fmt.Sprintf("the following source returned a bad response: %s", e.Source)
}

// RegistryError reports an HTTP-level failure talking to a registry.
type RegistryError struct {
	StatusCode int
	Detail     string
}

func (e *RegistryError) Error() string {
	return fmt.Sprintf("registry error %d: %s", e.StatusCode, e.Detail)
}

// BranchNotFoundError reports a job branch that does not exist.
type BranchNotFoundError struct {
	Branch string
}

func (e *BranchNotFoundError) Error() string {
	return fmt.Sprintf("branch %s not found", e.Branch)
}

// NotImplementedError reports an operation an ecosystem does not support.
type NotImplementedError struct {
	Operation string
}

func (e *NotImplementedError) Error() string {
	return fmt.Sprintf("%s is not implemented for this ecosystem", e.Operation)
}
