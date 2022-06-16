package store

import (
	"context"
	"fmt"
	"time"

	"github.com/keegancsmith/sqlf"
	"github.com/lib/pq"
	"github.com/opentracing/opentracing-go/log"

	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/codeintel/dependencies/shared"
	"github.com/sourcegraph/sourcegraph/internal/codeintel/types"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/database/basestore"
	"github.com/sourcegraph/sourcegraph/internal/database/batch"
	"github.com/sourcegraph/sourcegraph/internal/database/dbutil"
	"github.com/sourcegraph/sourcegraph/internal/observation"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

// Store provides the interface for package dependencies storage.
type Store interface {
	PreciseDependencies(ctx context.Context, repoName, commit string) (deps map[api.RepoName]types.RevSpecSet, err error)
	PreciseDependents(ctx context.Context, repoName, commit string) (deps map[api.RepoName]types.RevSpecSet, err error)
	LockfileDependencies(ctx context.Context, repoName, commit string) (deps []shared.PackageDependency, found bool, err error)
	UpsertLockfileDependencies(ctx context.Context, repoName, commit string, deps []shared.PackageDependency) (err error)
	UpsertLockfileGraph(ctx context.Context, repoName, commit string, deps []shared.PackageDependency, graph shared.DependencyGraph) (err error)
	SelectRepoRevisionsToResolve(ctx context.Context, batchSize int, minimumCheckInterval time.Duration) (_ map[string][]string, err error)
	UpdateResolvedRevisions(ctx context.Context, repoRevsToResolvedRevs map[string]map[string]string) (err error)
	LockfileDependents(ctx context.Context, repoName, commit string) (deps []api.RepoCommit, err error)
	ListDependencyRepos(ctx context.Context, opts ListDependencyReposOpts) (dependencyRepos []shared.Repo, err error)
	UpsertDependencyRepos(ctx context.Context, deps []shared.Repo) (newDeps []shared.Repo, err error)
	DeleteDependencyReposByID(ctx context.Context, ids ...int) (err error)
}

// store manages the database tables for package dependencies.
type store struct {
	db         *basestore.Store
	operations *operations
}

// New returns a new store.
func New(db database.DB, op *observation.Context) *store {
	return &store{
		db:         basestore.NewWithHandle(db.Handle()),
		operations: newOperations(op),
	}
}

// PreciseDependencies returns package dependencies from precise indexes. It is assumed that
// the given commit is the canonical 40-character hash.
func (s *store) PreciseDependencies(ctx context.Context, repoName, commit string) (deps map[api.RepoName]types.RevSpecSet, err error) {
	ctx, _, endObservation := s.operations.preciseDependencies.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("repoName", repoName),
		log.String("commit", commit),
	}})
	defer endObservation(1, observation.Args{})

	return scanRepoRevSpecSets(s.db.Query(ctx, sqlf.Sprintf(preciseDependenciesQuery, repoName, commit)))
}

const preciseDependenciesQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:PreciseDependencies
SELECT pr.name, pu.commit
FROM lsif_packages lp
JOIN lsif_uploads pu ON pu.id = lp.dump_id
JOIN repo pr ON pr.id = pu.repository_id
JOIN lsif_references lr ON lr.scheme = lp.scheme AND lr.name = lp.name AND lr.version = lp.version
JOIN lsif_uploads ru ON ru.id = lr.dump_id
JOIN repo rr ON rr.id = ru.repository_id
WHERE rr.name = %s AND ru.commit = %s
`

// PreciseDependents returns package dependents from precise indexes. It is assumed that
// the given commit is the canonical 40-character hash.
func (s *store) PreciseDependents(ctx context.Context, repoName, commit string) (deps map[api.RepoName]types.RevSpecSet, err error) {
	ctx, _, endObservation := s.operations.preciseDependents.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("repoName", repoName),
		log.String("commit", commit),
	}})
	defer endObservation(1, observation.Args{})

	return scanRepoRevSpecSets(s.db.Query(ctx, sqlf.Sprintf(preciseDependentsQuery, repoName, commit)))
}

const preciseDependentsQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:PreciseDependents
SELECT rr.name, ru.commit
FROM lsif_packages lp
JOIN lsif_uploads pu ON pu.id = lp.dump_id
JOIN repo pr ON pr.id = pu.repository_id
JOIN lsif_references lr ON lr.scheme = lp.scheme AND lr.name = lp.name AND lr.version = lp.version
JOIN lsif_uploads ru ON ru.id = lr.dump_id
JOIN repo rr ON rr.id = ru.repository_id
WHERE pr.name = %s AND pu.commit = %s
`

const recursiveLockfileDependenciesQuery = `
WITH RECURSIVE dependencies(id, depends_on, level, max_level) AS (
  SELECT
    id, depends_on, 0 AS level, 3 AS max_level
  FROM
    codeintel_lockfile_references
  WHERE
    ARRAY [id] @> (SELECT codeintel_lockfile_reference_ids FROM codeintel_lockfiles WHERE repository_id = 5)

  UNION ALL

  SELECT
    lr.id, lr.depends_on, (dependencies.level+1) AS level, dependencies.max_level
  FROM
    codeintel_lockfile_references lr
  JOIN dependencies ON lr.id = ANY (dependencies.depends_on)
  WHERE
    level < dependencies.max_level
)
SELECT
  dependencies.level, lr.*
FROM
  dependencies, codeintel_lockfile_references lr
WHERE
  dependencies.id = lr.id;
`

// LockfileDependencies returns package dependencies from a previous lockfiles result for
// the given repository and commit. It is assumed that the given commit is the canonical
// 40-character hash.
func (s *store) LockfileDependencies(ctx context.Context, repoName, commit string) (deps []shared.PackageDependency, found bool, err error) {
	ctx, _, endObservation := s.operations.lockfileDependencies.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("repoName", repoName),
		log.String("commit", commit),
	}})
	defer func() {
		endObservation(1, observation.Args{LogFields: []log.Field{
			log.Bool("found", found),
			log.Int("numDeps", len(deps)),
		}})
	}()

	tx, err := s.Transact(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = tx.db.Done(err) }()

	resolutionID := fmt.Sprintf("resolution-%s-%s", repoName, commit)

	deps, err = scanPackageDependencies(tx.db.Query(ctx, sqlf.Sprintf(
		lockfileDependenciesQuery,
		repoName,
		dbutil.CommitBytea(commit),
		resolutionID,
	)))
	if err != nil {
		return nil, false, err
	}
	if len(deps) == 0 {
		// No dependencies were found, but we could have already written a record
		// that just had an empty references list. Check to see if this is the case
		// so we don't attempt to re-parse the lockfiles of this repo/commit from the
		// dependencies service.
		_, found, err = basestore.ScanFirstInt(tx.db.Query(ctx, sqlf.Sprintf(
			lockfileDependenciesExistsQuery,
			repoName,
			dbutil.CommitBytea(commit),
		)))

		return nil, found, err
	}

	return deps, true, nil
}

const lockfileDependenciesQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:LockfileDependencies
SELECT
	repository_name,
	revspec,
	package_scheme,
	package_name,
	package_version
FROM codeintel_lockfile_references
WHERE id IN (
	SELECT DISTINCT unnest(codeintel_lockfile_reference_ids) AS id
	FROM codeintel_lockfiles
	WHERE repository_id = (SELECT id FROM repo WHERE name = %s)
	AND commit_bytea = %s
	AND resolution_id = %s
)
ORDER BY repository_name, revspec
`

const lockfileDependenciesExistsQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:LockfileDependencies
SELECT 1
FROM codeintel_lockfiles
WHERE repository_id = (SELECT id FROM repo WHERE name = %s) AND commit_bytea = %s
`

// UpsertLockfileDependencies inserts the given package dependencies if they do not exist
// and inserts a new lockfiles result for the given repository and commit. It is assumed
// that the given commit is the canonical 40-character hash.
func (s *store) UpsertLockfileDependencies(ctx context.Context, repoName, commit string, deps []shared.PackageDependency) (err error) {
	ctx, _, endObservation := s.operations.upsertLockfileDependencies.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("repoName", repoName),
		log.String("commit", commit),
		log.Int("numDeps", len(deps)),
	}})
	defer endObservation(1, observation.Args{})

	tx, err := s.Transact(ctx)
	if err != nil {
		return err
	}
	defer func() { err = tx.db.Done(err) }()

	if err := tx.db.Exec(ctx, sqlf.Sprintf(temporaryLockfileReferencesTableQuery)); err != nil {
		return err
	}

	// TODO: Fix this
	resolutionID := fmt.Sprintf("resolution-%s-%s", repoName, commit)
	if err := batch.InsertValues(
		ctx,
		tx.db.Handle().DB(),
		"t_codeintel_lockfile_references",
		batch.MaxNumPostgresParameters,
		[]string{"repository_name", "revspec", "package_scheme", "package_name", "package_version", "depends_on", "resolution_id"},
		populatePackageDependencyChannel(deps, resolutionID),
	); err != nil {
		return err
	}

	ids, err := basestore.ScanInts(tx.db.Query(ctx, sqlf.Sprintf(upsertLockfileReferencesQuery)))
	if err != nil {
		return err
	}
	if ids == nil {
		ids = []int{}
	}
	idsArray := pq.Array(ids)

	return tx.db.Exec(ctx, sqlf.Sprintf(
		insertLockfilesQuery,
		dbutil.CommitBytea(commit),
		idsArray,
		resolutionID,
		repoName,
		idsArray,
	))
}

const temporaryLockfileReferencesTableQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:UpsertLockfileDependencies
CREATE TEMPORARY TABLE t_codeintel_lockfile_references (
	repository_name text NOT NULL,
	revspec text NOT NULL,
	package_scheme text NOT NULL,
	package_name text NOT NULL,
	package_version text NOT NULL,
	depends_on integer[] NOT NULL,
	resolution_id text NOT NULL
) ON COMMIT DROP
`

const upsertLockfileReferencesQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:UpsertLockfileDependencies
WITH ins AS (
	INSERT INTO codeintel_lockfile_references (repository_name, revspec, package_scheme, package_name, package_version, depends_on, resolution_id)
	SELECT repository_name, revspec, package_scheme, package_name, package_version, depends_on, resolution_id FROM t_codeintel_lockfile_references
	ON CONFLICT DO NOTHING
	RETURNING id
),
duplicates AS (
	SELECT id
	FROM t_codeintel_lockfile_references t
	JOIN codeintel_lockfile_references r
	ON
		r.repository_name = t.repository_name AND
		r.revspec = t.revspec AND
		r.package_scheme = t.package_scheme AND
		r.package_name = t.package_name AND
		r.package_version = t.package_version AND
		r.resolution_id = t.resolution_id
		-- We ignore depends_on since that is updated in a second query and we can't use it to compare
)
SELECT id FROM ins UNION
SELECT id FROM duplicates
ORDER BY id
`

const upsertLockfileReferences2Query = `
-- source: internal/codeintel/dependencies/internal/store/store.go:UpsertLockfileDependencies
WITH ins AS (
	INSERT INTO codeintel_lockfile_references (repository_name, revspec, package_scheme, package_name, package_version, depends_on, resolution_id)
	SELECT repository_name, revspec, package_scheme, package_name, package_version, depends_on, resolution_id
	FROM t_codeintel_lockfile_references
	ON CONFLICT DO NOTHING
	RETURNING id, package_name
),
duplicates AS (
	SELECT r.id, r.package_name
	FROM t_codeintel_lockfile_references t
	JOIN codeintel_lockfile_references r
	ON
		r.repository_name = t.repository_name AND
		r.revspec = t.revspec AND
		r.package_scheme = t.package_scheme AND
		r.package_name = t.package_name AND
		r.package_version = t.package_version AND
		r.resolution_id = t.resolution_id
		-- We ignore depends_on since that is updated in a second query and we can't use it to compare
)
SELECT id, package_name FROM ins UNION
SELECT id, package_name FROM duplicates
ORDER BY id
`

const insertLockfilesQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:UpsertLockfileDependencies
INSERT INTO codeintel_lockfiles (
	repository_id,
	commit_bytea,
	codeintel_lockfile_reference_ids,
	resolution_id
)
SELECT id, %s, %s, %s
FROM repo
WHERE name = %s
-- Last write wins
ON CONFLICT (repository_id, commit_bytea) DO UPDATE
SET codeintel_lockfile_reference_ids = %s
`

// populatePackageDependencyChannel populates a channel with the given dependencies for bulk insertion.
func populatePackageDependencyChannel(deps []shared.PackageDependency, resolutionId string) <-chan []any {
	ch := make(chan []any, len(deps))

	go func() {
		defer close(ch)

		for _, dep := range deps {
			ch <- []any{
				dep.RepoName(),
				dep.GitTagFromVersion(),
				dep.Scheme(),
				dep.PackageSyntax(),
				dep.PackageVersion(),
				pq.Array([]int{}),
				resolutionId,
			}
		}
	}()

	return ch
}

// UpsertLockfileGraph TODO
func (s *store) UpsertLockfileGraph(ctx context.Context, repoName, commit string, deps []shared.PackageDependency, graph shared.DependencyGraph) (err error) {
	ctx, _, endObservation := s.operations.upsertLockfileDependencies.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("repoName", repoName),
		log.String("commit", commit),
	}})
	defer endObservation(1, observation.Args{})

	// TODO: All of this in here is not as efficient as it could be

	resolutionID := fmt.Sprintf("resolution-%s-%s", repoName, commit)

	tx, err := s.Transact(ctx)
	if err != nil {
		return err
	}
	defer func() { err = tx.db.Done(err) }()

	if err := tx.db.Exec(ctx, sqlf.Sprintf(temporaryLockfileReferencesTableQuery)); err != nil {
		return err
	}

	//
	// Step 1: insert all packages into codeintel_lockfile_references table, return their names/ids
	//
	if err := batch.InsertValues(
		ctx,
		tx.db.Handle().DB(),
		"t_codeintel_lockfile_references",
		batch.MaxNumPostgresParameters,
		[]string{"repository_name", "revspec", "package_scheme", "package_name", "package_version", "depends_on", "resolution_id"},
		populatePackageDependencyChannel(deps, resolutionID),
	); err != nil {
		return err
	}

	// Get IDs and name->ID mapping for upserted packages
	nameIDs, ids, err := scanIdNames(tx.db.Query(ctx, sqlf.Sprintf(upsertLockfileReferences2Query)))
	if err != nil {
		return err
	}

	// If we don't have a graph, we insert all of the dependencies as direct
	// dependencies and return.
	if graph.Empty() {
		idsArray := pq.Array(ids)
		return tx.db.Exec(ctx, sqlf.Sprintf(
			insertLockfilesQuery,
			dbutil.CommitBytea(commit),
			idsArray,
			resolutionID,
			repoName,
			idsArray,
		))
	}

	//
	// Step 2: collect all the dependencies (i.e. pkg-A depends on B, C, D) and map them to database IDs
	//
	dependencies := make(map[int][]int)
	for _, edge := range graph.AllEdges() {
		sourceName, targetName := edge[0].PackageSyntax(), edge[1].PackageSyntax()

		sourceID, ok := nameIDs[sourceName]
		if !ok {
			return errors.Newf("id for source %s not found", sourceName)
		}

		targetID, ok := nameIDs[targetName]
		if !ok {
			return errors.Newf("id for target %s not found", sourceName)
		}

		if ids, ok := dependencies[sourceID]; !ok {
			dependencies[sourceID] = []int{targetID}
		} else {
			dependencies[sourceID] = append(ids, targetID)
		}
	}

	// Insert edges into DB. TODO: We could/should batch this
	for source, targets := range dependencies {
		if err := tx.db.Exec(ctx, sqlf.Sprintf(
			insertLockfilesEdgesQuery,
			pq.Array(targets),
			source,
			resolutionID,
		)); err != nil {
			return err
		}
	}

	//
	// Step 3: insert codeintel_lockfile, pointing to the roots of the graph (i.e. direct dependencies)
	//
	var roots []int
	for _, r := range graph.Roots() {
		name := r.PackageSyntax()
		id, ok := nameIDs[name]
		if !ok {
			return errors.Newf("id for root %s not found", name)
		}
		roots = append(roots, id)
	}

	idsArray := pq.Array(roots)
	return tx.db.Exec(ctx, sqlf.Sprintf(
		insertLockfilesQuery,
		dbutil.CommitBytea(commit),
		idsArray,
		resolutionID,
		repoName,
		idsArray,
	))
}

const insertLockfilesEdgesQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:InsertLockfileGraph
UPDATE codeintel_lockfile_references
SET depends_on = %s
WHERE
	id = %s
AND
	resolution_id = %s
`

const insertLockfileWithRootsQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:InsertLockfileGraph
INSERT INTO codeintel_lockfiles (
	repository_id,
	commit_bytea,
	codeintel_lockfile_reference_ids,
	resolution_id
)
SELECT id, %s, %s, %s
FROM repo
WHERE name = %s
-- Last write wins
ON CONFLICT (repository_id, commit_bytea) DO UPDATE
SET codeintel_lockfile_reference_ids = %s
`

// SelectRepoRevisionsToResolve selects the references lockfile packages to
// possibly resolve them to repositories on the Sourcegraph instance.
func (s *store) SelectRepoRevisionsToResolve(ctx context.Context, batchSize int, minimumCheckInterval time.Duration) (_ map[string][]string, err error) {
	return s.selectRepoRevisionsToResolve(ctx, batchSize, minimumCheckInterval, time.Now())
}

func (s *store) selectRepoRevisionsToResolve(ctx context.Context, batchSize int, minimumCheckInterval time.Duration, now time.Time) (_ map[string][]string, err error) {
	var count int
	ctx, _, endObservation := s.operations.selectRepoRevisionsToResolve.With(ctx, &err, observation.Args{})
	defer endObservation(1, observation.Args{
		LogFields: []log.Field{
			log.Int("count", count),
		},
	})

	rows, err := s.db.Query(ctx, sqlf.Sprintf(selectRepoRevisionsToResolveQuery, now, int64(minimumCheckInterval/time.Hour), batchSize, now))
	if err != nil {
		return nil, err
	}
	defer func() { err = basestore.CloseRows(rows, err) }()

	m := map[string][]string{}
	for rows.Next() {
		var repositoryName, commit string
		if err := rows.Scan(&repositoryName, &commit); err != nil {
			return nil, err
		}

		count++
		m[repositoryName] = append(m[repositoryName], commit)
	}

	return m, nil
}

const selectRepoRevisionsToResolveQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:SelectRepoRevisionsToResolve
WITH candidates AS (
	SELECT
		repository_name,
		revspec
	FROM codeintel_lockfile_references
	WHERE
		last_check_at IS NULL OR
		%s - last_check_at >= (%s * '1 hour'::interval)
	GROUP BY repository_name, revspec
	ORDER BY repository_name, revspec
	-- TODO - select for update to reduce contention
	LIMIT %s
),
updated AS (
	UPDATE codeintel_lockfile_references
	SET last_check_at = %s
	WHERE (repository_name, revspec) IN (SELECT * FROM candidates)
)
SELECT * FROM candidates
`

// UpdateResolvedRevisions updates the lockfile packages that were resolved to
// repositories/revisions pairs on the Sourcegraph instance.
func (s *store) UpdateResolvedRevisions(ctx context.Context, repoRevsToResolvedRevs map[string]map[string]string) (err error) {
	ctx, _, endObservation := s.operations.updateResolvedRevisions.With(ctx, &err, observation.Args{})
	defer endObservation(1, observation.Args{})

	for repoName, resolvedRevs := range repoRevsToResolvedRevs {
		for commit, resolvedCommit := range resolvedRevs {
			// TODO - batch these updates
			if err := s.db.Exec(ctx, sqlf.Sprintf(
				updateResolvedRevisionsQuery,
				repoName,
				dbutil.CommitBytea(resolvedCommit),
				repoName,
				commit,
			)); err != nil {
				return err
			}
		}
	}

	return nil
}

const updateResolvedRevisionsQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:UpdateResolvedRevisions
UPDATE
	codeintel_lockfile_references
SET
	repository_id = (SELECT id FROM repo WHERE name = %s),
	commit_bytea = %s
WHERE
	repository_name = %s AND
	revspec = %s
-- TODO - order before update to reduce contention
`

// LockfileDependents returns the set of repositories that have lockfile results pointing to the
// given repo and commit (related to a particular resolved repo/commit of a lockfile reference).
func (s *store) LockfileDependents(ctx context.Context, repoName, commit string) (deps []api.RepoCommit, err error) {
	ctx, _, endObservation := s.operations.lockfileDependents.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("repoName", repoName),
		log.String("commit", commit),
	}})
	defer func() {
		endObservation(1, observation.Args{LogFields: []log.Field{
			log.Int("numDependencies", len(deps)),
		}})
	}()

	return scanRepoCommits(s.db.Query(ctx, sqlf.Sprintf(lockfileDependentsQuery, repoName, dbutil.CommitBytea(commit))))
}

const lockfileDependentsQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:LockfileDependents
SELECT r.name, encode(lf.commit_bytea, 'hex') AS commit
FROM codeintel_lockfile_references lr
JOIN codeintel_lockfiles lf ON LF.codeintel_lockfile_reference_ids @> ARRAY [ lr.id ] AND lf.resolution_id = lr.resolution_id
JOIN repo r ON r.id = lf.repository_id
JOIN repo rr ON rr.id = lr.repository_id
WHERE rr.name = %s AND lr.commit_bytea = %s
ORDER BY r.name, lf.commit_bytea
`

// ListDependencyReposOpts are options for listing dependency repositories.
type ListDependencyReposOpts struct {
	Scheme      string
	Name        string
	After       int
	Limit       int
	NewestFirst bool
}

// ListDependencyRepos returns dependency repositories to be synced by gitserver.
func (s *store) ListDependencyRepos(ctx context.Context, opts ListDependencyReposOpts) (dependencyRepos []shared.Repo, err error) {
	ctx, _, endObservation := s.operations.listDependencyRepos.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.String("scheme", opts.Scheme),
	}})
	defer func() {
		endObservation(1, observation.Args{LogFields: []log.Field{
			log.Int("numDependencyRepos", len(dependencyRepos)),
		}})
	}()

	sortDirection := "ASC"
	if opts.NewestFirst {
		sortDirection = "DESC"
	}

	return scanDependencyRepos(s.db.Query(ctx, sqlf.Sprintf(
		listDependencyReposQuery,
		sqlf.Join(makeListDependencyReposConds(opts), "AND"),
		sqlf.Sprintf(sortDirection),
		makeLimit(opts.Limit),
	)))
}

const listDependencyReposQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:ListDependencyRepos
SELECT id, scheme, name, version
FROM lsif_dependency_repos
WHERE %s
ORDER BY id %s
%s
`

func makeListDependencyReposConds(opts ListDependencyReposOpts) []*sqlf.Query {
	conds := make([]*sqlf.Query, 0, 3)
	conds = append(conds, sqlf.Sprintf("scheme = %s", opts.Scheme))

	if opts.Name != "" {
		conds = append(conds, sqlf.Sprintf("name = %s", opts.Name))
	}
	if opts.After != 0 {
		if opts.NewestFirst {
			conds = append(conds, sqlf.Sprintf("id < %s", opts.After))
		} else {
			conds = append(conds, sqlf.Sprintf("id > %s", opts.After))
		}
	}

	return conds
}

func makeLimit(limit int) *sqlf.Query {
	if limit == 0 {
		return sqlf.Sprintf("")
	}

	return sqlf.Sprintf("LIMIT %s", limit)
}

// UpsertDependencyRepos creates the given dependency repos if they don't yet exist. The values
// that did not exist previously are returned.
func (s *store) UpsertDependencyRepos(ctx context.Context, deps []shared.Repo) (newDeps []shared.Repo, err error) {
	ctx, _, endObservation := s.operations.upsertDependencyRepos.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.Int("numDeps", len(deps)),
	}})
	defer func() {
		endObservation(1, observation.Args{LogFields: []log.Field{
			log.Int("numNewDeps", len(newDeps)),
		}})
	}()

	callback := func(inserter *batch.Inserter) error {
		for _, dep := range deps {
			if err := inserter.Insert(ctx, dep.Scheme, dep.Name, dep.Version); err != nil {
				return err
			}
		}

		return nil
	}

	returningScanner := func(rows dbutil.Scanner) error {
		dependencyRepo, err := scanDependencyRepo(rows)
		if err != nil {
			return err
		}

		newDeps = append(newDeps, dependencyRepo)
		return nil
	}

	err = batch.WithInserterWithReturn(
		ctx,
		s.db.Handle().DB(),
		"lsif_dependency_repos",
		batch.MaxNumPostgresParameters,
		[]string{"scheme", "name", "version"},
		"ON CONFLICT DO NOTHING",
		[]string{"id", "scheme", "name", "version"},
		returningScanner,
		callback,
	)
	return newDeps, err
}

// DeleteDependencyReposByID removes the dependency repos with the given ids, if they exist.
func (s *store) DeleteDependencyReposByID(ctx context.Context, ids ...int) (err error) {
	ctx, _, endObservation := s.operations.deleteDependencyReposByID.With(ctx, &err, observation.Args{LogFields: []log.Field{
		log.Int("numIDs", len(ids)),
	}})
	defer endObservation(1, observation.Args{})

	if len(ids) == 0 {
		return nil
	}

	return s.db.Exec(ctx, sqlf.Sprintf(deleteDependencyReposByIDQuery, pq.Array(ids)))
}

const deleteDependencyReposByIDQuery = `
-- source: internal/codeintel/dependencies/internal/store/store.go:DeleteDependencyReposByID
DELETE FROM lsif_dependency_repos
WHERE id = ANY(%s)
`

// Transact returns a store in a transaction.
func (s *store) Transact(ctx context.Context) (*store, error) {
	txBase, err := s.db.Transact(ctx)
	if err != nil {
		return nil, err
	}

	return &store{
		db:         txBase,
		operations: s.operations,
	}, nil
}
