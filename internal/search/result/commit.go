package result

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/xeonx/timeago"

	"github.com/sourcegraph/sourcegraph/internal/gitserver/gitdomain"
	"github.com/sourcegraph/sourcegraph/internal/search/filter"
	"github.com/sourcegraph/sourcegraph/internal/types"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

type CommitMatch struct {
	Commit gitdomain.Commit
	Repo   types.MinimalRepo

	// Refs is a set of git references that point to this commit. For example,
	// for a search like `repo:sourcegraph@abcd123`, if the `refs/heads/main`
	// branch currently points to commit `abcd123`, Refs will contain `main`.
	// Note: this might be empty because finding refs that point to a commit
	// is an expensive operation that may be disabled.
	Refs []string

	// SourceRefs is the set of input refs that were used to find this commit.
	// For example, with a search like `repo:sourcegraph@my-branch`, SourceRefs
	// should be set to []string{"my-branch"}
	SourceRefs []string

	// MessagePreview and DiffPreview are mutually exclusive. Only one should be set
	MessagePreview *MatchedString
	DiffPreview    *MatchedString

	// ModifiedFiles will include the list of files modified in the commit when
	// sub-repo permissions filtering has been enabled.
	ModifiedFiles []string
}

func (cm *CommitMatch) Body() MatchedString {
	if cm.DiffPreview != nil {
		return MatchedString{
			Content:       "```diff\n" + cm.DiffPreview.Content + "\n```",
			MatchedRanges: cm.DiffPreview.MatchedRanges.Add(Location{Line: 1, Offset: len("```diff\n")}),
		}
	}

	return MatchedString{
		Content:       "```COMMIT_EDITMSG\n" + cm.MessagePreview.Content + "\n```",
		MatchedRanges: cm.MessagePreview.MatchedRanges.Add(Location{Line: 1, Offset: len("```COMMIT_EDITMSG\n")}),
	}
}

// ResultCount for CommitSearchResult returns the number of highlights if there
// are highlights and 1 otherwise. We implemented this method because we want to
// return a more meaningful result count for streaming while maintaining backward
// compatibility for our GraphQL API. The GraphQL API calls ResultCount on the
// resolver, while streaming calls ResultCount on CommitSearchResult.
func (cm *CommitMatch) ResultCount() int {
	matchCount := 0
	switch {
	case cm.DiffPreview != nil:
		matchCount = len(cm.DiffPreview.MatchedRanges)
	case cm.MessagePreview != nil:
		matchCount = len(cm.MessagePreview.MatchedRanges)
	}
	if matchCount > 0 {
		return matchCount
	}
	// Queries such as type:commit after:"1 week ago" don't have highlights. We count
	// those results as 1.
	return 1
}

func (cm *CommitMatch) RepoName() types.MinimalRepo {
	return cm.Repo
}

func (cm *CommitMatch) Limit(limit int) int {
	limitMatchedString := func(ms *MatchedString) int {
		if len(ms.MatchedRanges) == 0 {
			return limit - 1
		} else if len(ms.MatchedRanges) > limit {
			ms.MatchedRanges = ms.MatchedRanges[:limit]
			return 0
		}
		return limit - len(ms.MatchedRanges)
	}

	switch {
	case cm.DiffPreview != nil:
		return limitMatchedString(cm.DiffPreview)
	case cm.MessagePreview != nil:
		return limitMatchedString(cm.MessagePreview)
	default:
		panic("exactly one of DiffPreview or Message must be set")
	}
}

func (cm *CommitMatch) Select(path filter.SelectPath) Match {
	switch path.Root() {
	case filter.Repository:
		return &RepoMatch{
			Name: cm.Repo.Name,
			ID:   cm.Repo.ID,
		}
	case filter.Commit:
		fields := path[1:]
		if len(fields) > 0 && fields[0] == "diff" {
			if cm.DiffPreview == nil {
				return nil // Not a diff result.
			}
			if len(fields) == 1 {
				return cm
			}
			if len(fields) == 2 {
				return selectCommitDiffKind(cm, fields[1])
			}
			return nil
		}
		return cm
	}
	return nil
}

// AppendMatches merges highlight information for commit messages. Diff contents
// are not currently supported. TODO(@team/search): Diff highlight information
// cannot reliably merge this way because of offset issues with markdown
// rendering.
func (cm *CommitMatch) AppendMatches(src *CommitMatch) {
	if cm.MessagePreview != nil && src.MessagePreview != nil {
		cm.MessagePreview.MatchedRanges = append(cm.MessagePreview.MatchedRanges, src.MessagePreview.MatchedRanges...)
	}
}

// Key implements Match interface's Key() method
func (cm *CommitMatch) Key() Key {
	typeRank := rankCommitMatch
	if cm.DiffPreview != nil {
		typeRank = rankDiffMatch
	}
	return Key{
		TypeRank:   typeRank,
		Repo:       cm.Repo.Name,
		AuthorDate: cm.Commit.Author.Date,
		Commit:     cm.Commit.ID,
	}
}

func (cm *CommitMatch) Label() string {
	message := cm.Commit.Message.Subject()
	author := cm.Commit.Author.Name
	repoName := displayRepoName(string(cm.Repo.Name))
	repoURL := (&RepoMatch{Name: cm.Repo.Name, ID: cm.Repo.ID}).URL().String()
	commitURL := cm.URL().String()

	return fmt.Sprintf("[%s](%s) › [%s](%s): [%s](%s)", repoName, repoURL, author, commitURL, message, commitURL)
}

func (cm *CommitMatch) Detail() string {
	commitHash := cm.Commit.ID.Short()
	timeagoConfig := timeago.NoMax(timeago.English)
	return fmt.Sprintf("[`%v` %v](%v)", commitHash, timeagoConfig.Format(cm.Commit.Author.Date), cm.URL())
}

func (cm *CommitMatch) URL() *url.URL {
	u := (&RepoMatch{Name: cm.Repo.Name, ID: cm.Repo.ID}).URL()
	u.Path = u.Path + "/-/commit/" + string(cm.Commit.ID)
	return u
}

func displayRepoName(repoPath string) string {
	parts := strings.Split(repoPath, "/")
	if len(parts) >= 3 && strings.Contains(parts[0], ".") {
		parts = parts[1:] // remove hostname from repo path (reduce visual noise)
	}
	return strings.Join(parts, "/")
}

// selectModifiedLines extracts the highlight ranges that correspond to lines
// that have a `+` or `-` prefix (corresponding to additions resp. removals).
func selectModifiedLines(lines []string, highlights []Range, prefix string) []Range {
	if len(lines) == 0 {
		return highlights
	}
	include := make([]Range, 0, len(highlights))
	for _, h := range highlights {
		if h.Start.Line < 0 {
			// Skip negative line numbers. See: https://github.com/sourcegraph/sourcegraph/issues/20286.
			continue
		}
		if strings.HasPrefix(lines[h.Start.Line], prefix) {
			include = append(include, h)
		}
	}
	return include
}

// modifiedLinesExist checks whether any `line` in lines starts with `prefix`.
func modifiedLinesExist(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// selectCommitDiffKind returns a commit match `c` if it contains `added` (resp.
// `removed`) lines set by `field. It ensures that highlight information only
// applies to the modified lines selected by `field`. If there are no matches
// (i.e., no highlight information) coresponding to modified lines, it is
// removed from the result set (returns nil).
func selectCommitDiffKind(c *CommitMatch, field string) Match {
	diff := c.DiffPreview
	if diff == nil {
		return nil // Not a diff result.
	}
	var prefix string
	if field == "added" {
		prefix = "+"
	}
	if field == "removed" {
		prefix = "-"
	}
	if len(diff.MatchedRanges) == 0 {
		// No highlights, implying no pattern was specified. Filter by
		// whether there exists lines corresponding to additions or
		// removals.
		if modifiedLinesExist(strings.Split(diff.Content, "\n"), prefix) {
			return c
		}
		return nil
	}
	diffHighlights := selectModifiedLines(strings.Split(diff.Content, "\n"), diff.MatchedRanges, prefix)
	if len(diffHighlights) > 0 {
		c.DiffPreview.MatchedRanges = diffHighlights
		return c
	}
	return nil // No matching lines.
}

func (r *CommitMatch) searchResultMarker() {}

func parseDiffString(diff string) (res []diffFile, err error) {
	const (
		INIT = iota
		IN_DIFF
		IN_HUNK
	)

	state := INIT
	var currentDiff diffFile
	var currentHunk diffHunk
	for _, line := range strings.Split(diff, "\n") {
		if len(line) == 0 {
			continue
		}
		switch state {
		case INIT:
			currentDiff.oldFile, currentDiff.newFile, err = splitDiffFiles(line)
			state = IN_DIFF
		case IN_DIFF:
			currentHunk.oldStart, currentHunk.oldCount, currentHunk.newStart, currentHunk.newCount, currentHunk.header, err = parseHunkHeader(line)
			state = IN_HUNK
		case IN_HUNK:
			switch line[0] {
			case '-', '+', ' ':
				currentHunk.lines = append(currentHunk.lines, line)
			case '@':
				currentDiff.hunks = append(currentDiff.hunks, currentHunk)
				currentHunk = diffHunk{}
				currentHunk.oldStart, currentHunk.oldCount, currentHunk.newStart, currentHunk.newCount, currentHunk.header, err = parseHunkHeader(line)
				state = IN_HUNK
			default:
				res = append(res, currentDiff)
				currentDiff.oldFile, currentDiff.newFile, err = splitDiffFiles(line)
				state = IN_DIFF
			}
		}
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}

var errInvalidDiff = errors.New("invalid diff format")

func splitDiffFiles(fileLine string) (oldFile, newFile string, err error) {
	split := strings.Fields(fileLine)
	if len(split) != 2 {
		return "", "", errInvalidDiff
	}
	return split[0], split[1], nil
}

var headerRegex = regexp.MustCompile(`@@ -(\d+),(\d+) \+(\d+),(\d+) @@ (.*)`)

func parseHunkHeader(headerLine string) (oldStart, oldCount, newStart, newCount int, header string, err error) {
	groups := headerRegex.FindStringSubmatch(headerLine)
	if groups == nil {
		return 0, 0, 0, 0, "", errInvalidDiff
	}
	oldStart, err = strconv.Atoi(groups[1])
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	oldCount, err = strconv.Atoi(groups[2])
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	newStart, err = strconv.Atoi(groups[3])
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	newCount, err = strconv.Atoi(groups[4])
	if err != nil {
		return 0, 0, 0, 0, "", err
	}
	return oldStart, oldCount, newStart, newCount, groups[5], nil
}

type diffFile struct {
	oldFile, newFile string
	hunks            []diffHunk
}

type diffHunk struct {
	oldStart, newStart int
	oldCount, newCount int
	header             string
	lines              []string
}
