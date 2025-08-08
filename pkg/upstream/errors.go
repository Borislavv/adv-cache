package upstream

import "errors"

// -----------------------------------------------------------------------------
// Errors.
// -----------------------------------------------------------------------------

var (
	ErrNoBackends                = errors.New("no healthy backends in cluster")
	ErrDuplicate                 = errors.New("backend already exists in cluster")
	ErrNotFound                  = errors.New("backend not found in cluster")
	ErrNilBackendConfig          = errors.New("nil backend config")
	ErrAllBackendsAreBusy        = errors.New("all backends are busy")
	ErrWorkflowStateMismatch     = errors.New("workflow state mismatch")
	ErrCannotAcquireIDForBackend = errors.New("cannot acquire ID for backend")
)
