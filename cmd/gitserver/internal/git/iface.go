package git

import (
	"context"
	"io"

	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/gitserver/gitdomain"
)

// GitBackend is the interface through which operations on a git repository can
// be performed. It encapsulates the underlying git implementation and allows
// us to test out alternative backends.
// A GitBackend is expected to be scoped to a specific repository directory at
// initialization time, ie. it should not be shared across various repositories.
type GitBackend interface {
	// Config returns a backend for interacting with git configuration at .git/config.
	Config() GitConfigBackend
	// GetObject allows to read a git object from the git object database.
	GetObject(ctx context.Context, objectName string) (*gitdomain.GitObject, error)
	// MergeBase finds the merge base commit for the given base and head revspecs.
	// Returns an empty string and no error if no common merge-base was found.
	MergeBase(ctx context.Context, baseRevspec, headRevspec string) (api.CommitID, error)

	// Exec is a temporary helper to run arbitrary git commands from the exec endpoint.
	// No new usages of it should be introduced and once the migration is done we will
	// remove this method.
	Exec(ctx context.Context, args ...string) (io.ReadCloser, error)
}

// GitConfigBackend provides methods for interacting with git configuration.
type GitConfigBackend interface {
	// Get reads a given config value. If the value is not set, it returns an
	// empty string and no error.
	Get(ctx context.Context, key string) (string, error)
	// Set sets a config value for the given key.
	Set(ctx context.Context, key, value string) error
	// Unset removes a config value of the given key. If the key wasn't present,
	// no error is returned.
	Unset(ctx context.Context, key string) error
}
