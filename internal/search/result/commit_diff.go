package result

import (
	"github.com/sourcegraph/go-diff/diff"

	"github.com/sourcegraph/sourcegraph/internal/gitserver/gitdomain"
	"github.com/sourcegraph/sourcegraph/internal/search/filter"
	"github.com/sourcegraph/sourcegraph/internal/types"
)

type CommitDiffMatch struct {
	Commit gitdomain.Commit
	Repo   types.MinimalRepo
	*diff.FileDiff
}

func (cd *CommitDiffMatch) RepoName() types.MinimalRepo {
	return cd.Repo
}

// Return a file path associated with this diff. If the file was created, it
// returns the created file path. IF the file was deleted, it returns the
// deleted file path. If modified, returns the modified path.
func (cm *CommitDiffMatch) Path() string {
	if cm.OrigName == "/dev/null" {
		return cm.NewName
	}
	if cm.NewName == "/dev/null" {
		return cm.OrigName
	}
	return cm.OrigName
}

// Key implements Match interface's Key() method
func (cm *CommitDiffMatch) Key() Key {
	var nonEmptyPath string
	pathStatus := Modified
	if cm.OrigName == "/dev/null" {
		nonEmptyPath = cm.NewName
		pathStatus = Added
	}
	if cm.NewName == "/dev/null" {
		nonEmptyPath = cm.OrigName
		pathStatus = Deleted
	}
	return Key{
		TypeRank:   rankDiffMatch,
		Repo:       cm.Repo.Name,
		AuthorDate: cm.Commit.Author.Date,
		Commit:     cm.Commit.ID,
		Path:       nonEmptyPath,
		PathStatus: pathStatus,
	}
}

func (cm *CommitDiffMatch) ResultCount() int {
	return 0 // TODO
}

func (cm *CommitDiffMatch) Limit(int) int {
	return 0 // TODO
}

func (cm *CommitDiffMatch) Select(filter.SelectPath) Match {
	return nil // TODO
}

func (cm *CommitDiffMatch) searchResultMarker() {

}
