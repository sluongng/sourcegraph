package dbstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/keegancsmith/sqlf"
	"github.com/lib/pq"

	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/commitgraph"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/stores/shared"
	"github.com/sourcegraph/sourcegraph/internal/database/basestore"
	"github.com/sourcegraph/sourcegraph/internal/database/dbutil"
	dbworkerstore "github.com/sourcegraph/sourcegraph/internal/workerutil/dbworker/store"
	"github.com/sourcegraph/sourcegraph/lib/codeintel/precise"
)

type printableRank struct{ value *int }

func (r printableRank) String() string {
	if r.value == nil {
		return "nil"
	}
	return strconv.Itoa(*r.value)
}

// makeCommit formats an integer as a 40-character git commit hash.
func makeCommit(i int) string {
	return fmt.Sprintf("%040d", i)
}

// insertUploads populates the lsif_uploads table with the given upload models.
func insertUploads(t testing.TB, db *sql.DB, uploads ...Upload) {
	for _, upload := range uploads {
		if upload.Commit == "" {
			upload.Commit = makeCommit(upload.ID)
		}
		if upload.State == "" {
			upload.State = "completed"
		}
		if upload.RepositoryID == 0 {
			upload.RepositoryID = 50
		}
		if upload.Indexer == "" {
			upload.Indexer = "lsif-go"
		}
		if upload.UploadedParts == nil {
			upload.UploadedParts = []int{}
		}

		// Ensure we have a repo for the inner join in select queries
		insertRepo(t, db, upload.RepositoryID, upload.RepositoryName)

		query := sqlf.Sprintf(`
			INSERT INTO lsif_uploads (
				id,
				commit,
				root,
				uploaded_at,
				state,
				failure_message,
				started_at,
				finished_at,
				process_after,
				num_resets,
				num_failures,
				repository_id,
				indexer,
				num_parts,
				uploaded_parts,
				upload_size,
				associated_index_id
			) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
		`,
			upload.ID,
			upload.Commit,
			upload.Root,
			upload.UploadedAt,
			upload.State,
			upload.FailureMessage,
			upload.StartedAt,
			upload.FinishedAt,
			upload.ProcessAfter,
			upload.NumResets,
			upload.NumFailures,
			upload.RepositoryID,
			upload.Indexer,
			upload.NumParts,
			pq.Array(upload.UploadedParts),
			upload.UploadSize,
			upload.AssociatedIndexID,
		)

		if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
			t.Fatalf("unexpected error while inserting upload: %s", err)
		}
	}
}

// insertIndexes populates the lsif_indexes table with the given index models.
func insertIndexes(t testing.TB, db *sql.DB, indexes ...Index) {
	for _, index := range indexes {
		if index.Commit == "" {
			index.Commit = makeCommit(index.ID)
		}
		if index.State == "" {
			index.State = "completed"
		}
		if index.RepositoryID == 0 {
			index.RepositoryID = 50
		}
		if index.DockerSteps == nil {
			index.DockerSteps = []DockerStep{}
		}
		if index.IndexerArgs == nil {
			index.IndexerArgs = []string{}
		}
		if index.LocalSteps == nil {
			index.LocalSteps = []string{}
		}

		// Ensure we have a repo for the inner join in select queries
		insertRepo(t, db, index.RepositoryID, index.RepositoryName)

		query := sqlf.Sprintf(`
			INSERT INTO lsif_indexes (
				id,
				commit,
				queued_at,
				state,
				failure_message,
				started_at,
				finished_at,
				process_after,
				num_resets,
				num_failures,
				repository_id,
				docker_steps,
				root,
				indexer,
				indexer_args,
				outfile,
				execution_logs,
				local_steps
			) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
		`,
			index.ID,
			index.Commit,
			index.QueuedAt,
			index.State,
			index.FailureMessage,
			index.StartedAt,
			index.FinishedAt,
			index.ProcessAfter,
			index.NumResets,
			index.NumFailures,
			index.RepositoryID,
			pq.Array(index.DockerSteps),
			index.Root,
			index.Indexer,
			pq.Array(index.IndexerArgs),
			index.Outfile,
			pq.Array(dbworkerstore.ExecutionLogEntries(index.ExecutionLogs)),
			pq.Array(index.LocalSteps),
		)

		if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
			t.Fatalf("unexpected error while inserting index: %s", err)
		}
	}
}

// insertRepo creates a repository record with the given id and name. If there is already a repository
// with the given identifier, nothing happens
func insertRepo(t testing.TB, db *sql.DB, id int, name string) {
	if name == "" {
		name = fmt.Sprintf("n-%d", id)
	}

	query := sqlf.Sprintf(
		`INSERT INTO repo (id, name) VALUES (%s, %s) ON CONFLICT (id) DO NOTHING`,
		id,
		name,
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while upserting repository: %s", err)
	}
}

// addToSearchContext creates a search context and adds the given repository to it. This is used to include
// repositories within tests for indexing candidacy.
func addToSearchContext(t testing.TB, db *sql.DB, id int) {
	query := sqlf.Sprintf(`
		WITH
		inserted AS (
			INSERT INTO search_contexts (name, description, public)
			VALUES (%s, '', false)
			RETURNING id
		)
		INSERT INTO search_context_repos (search_context_id, repo_id, revision)
		SELECT id, %s, '' FROM inserted
	`, fmt.Sprintf("test-context-%d", id), id)

	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while adding repository to search context: %s", err)
	}
}

// Marks a repo as deleted
func deleteRepo(t testing.TB, db *sql.DB, id int, deleted_at time.Time) {
	query := sqlf.Sprintf(
		`UPDATE repo SET deleted_at = %s WHERE id = %s`,
		deleted_at,
		id,
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while deleting repository: %s", err)
	}
}

// insertPackages populates the lsif_packages table with the given packages.
func insertPackages(t testing.TB, store *Store, packages []shared.Package) {
	for _, pkg := range packages {
		if err := store.UpdatePackages(context.Background(), pkg.DumpID, []precise.Package{
			{
				Scheme:  pkg.Scheme,
				Name:    pkg.Name,
				Version: pkg.Version,
			},
		}); err != nil {
			t.Fatalf("unexpected error updating packages: %s", err)
		}
	}
}

// insertPackageReferences populates the lsif_references table with the given package references.
func insertPackageReferences(t testing.TB, store *Store, packageReferences []shared.PackageReference) {
	for _, packageReference := range packageReferences {
		if err := store.UpdatePackageReferences(context.Background(), packageReference.DumpID, []precise.PackageReference{
			{
				Package: precise.Package{
					Scheme:  packageReference.Scheme,
					Name:    packageReference.Name,
					Version: packageReference.Version,
				},
				Filter: packageReference.Filter,
			},
		}); err != nil {
			t.Fatalf("unexpected error updating package references: %s", err)
		}
	}
}

// insertVisibleAtTip populates rows of the lsif_uploads_visible_at_tip table for the given repository
// with the given identifiers. Each upload is assumed to refer to the tip of the default branch. To mark
// an upload as protected (visible to _some_ branch) butn ot visible from the default branch, use the
// insertVisibleAtTipNonDefaultBranch method instead.
func insertVisibleAtTip(t testing.TB, db *sql.DB, repositoryID int, uploadIDs ...int) {
	insertVisibleAtTipInternal(t, db, repositoryID, true, uploadIDs...)
}

// insertVisibleAtTipNonDefaultBranch populates rows of the lsif_uploads_visible_at_tip table for the
// given repository with the given identifiers. Each upload is assumed to refer to the tip of a branch
// distinct from the default branch or a tag.
func insertVisibleAtTipNonDefaultBranch(t testing.TB, db *sql.DB, repositoryID int, uploadIDs ...int) {
	insertVisibleAtTipInternal(t, db, repositoryID, false, uploadIDs...)
}

func insertVisibleAtTipInternal(t testing.TB, db *sql.DB, repositoryID int, isDefaultBranch bool, uploadIDs ...int) {
	var rows []*sqlf.Query
	for _, uploadID := range uploadIDs {
		rows = append(rows, sqlf.Sprintf("(%s, %s, %s)", repositoryID, uploadID, isDefaultBranch))
	}

	query := sqlf.Sprintf(
		`INSERT INTO lsif_uploads_visible_at_tip (repository_id, upload_id, is_default_branch) VALUES %s`,
		sqlf.Join(rows, ","),
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while updating uploads visible at tip: %s", err)
	}
}

// insertNearestUploads populates the lsif_nearest_uploads table with the given upload metadata.
func insertNearestUploads(t testing.TB, db *sql.DB, repositoryID int, uploads map[string][]commitgraph.UploadMeta) {
	var rows []*sqlf.Query
	for commit, uploadMetas := range uploads {
		uploadsByLength := make(map[int]int, len(uploadMetas))
		for _, uploadMeta := range uploadMetas {
			uploadsByLength[uploadMeta.UploadID] = int(uploadMeta.Distance)
		}

		serializedUploadMetas, err := json.Marshal(uploadsByLength)
		if err != nil {
			t.Fatalf("unexpected error marshalling uploads: %s", err)
		}

		rows = append(rows, sqlf.Sprintf(
			"(%s, %s, %s)",
			repositoryID,
			dbutil.CommitBytea(commit),
			serializedUploadMetas,
		))
	}

	query := sqlf.Sprintf(
		`INSERT INTO lsif_nearest_uploads (repository_id, commit_bytea, uploads) VALUES %s`,
		sqlf.Join(rows, ","),
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while updating commit graph: %s", err)
	}
}

func insertLinks(t testing.TB, db *sql.DB, repositoryID int, links map[string]commitgraph.LinkRelationship) {
	if len(links) == 0 {
		return
	}

	var rows []*sqlf.Query
	for commit, link := range links {
		rows = append(rows, sqlf.Sprintf(
			"(%s, %s, %s, %s)",
			repositoryID,
			dbutil.CommitBytea(commit),
			dbutil.CommitBytea(link.AncestorCommit),
			link.Distance,
		))
	}

	query := sqlf.Sprintf(
		`INSERT INTO lsif_nearest_uploads_links (repository_id, commit_bytea, ancestor_commit_bytea, distance) VALUES %s`,
		sqlf.Join(rows, ","),
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while updating links: %s %s", err, query.Query(sqlf.PostgresBindVar))
	}
}

func toCommitGraphView(uploads []Upload) *commitgraph.CommitGraphView {
	commitGraphView := commitgraph.NewCommitGraphView()
	for _, upload := range uploads {
		commitGraphView.Add(commitgraph.UploadMeta{UploadID: upload.ID}, upload.Commit, fmt.Sprintf("%s:%s", upload.Root, upload.Indexer))
	}

	return commitGraphView
}

func getVisibleUploads(t testing.TB, db *sql.DB, repositoryID int, commits []string) map[string][]int {
	idsByCommit := map[string][]int{}
	for _, commit := range commits {
		query := makeVisibleUploadsQuery(repositoryID, commit)

		uploadIDs, err := basestore.ScanInts(db.QueryContext(
			context.Background(),
			query.Query(sqlf.PostgresBindVar),
			query.Args()...,
		))
		if err != nil {
			t.Fatalf("unexpected error getting visible upload IDs: %s", err)
		}
		sort.Ints(uploadIDs)

		idsByCommit[commit] = uploadIDs
	}

	return idsByCommit
}

func getUploadsVisibleAtTip(t testing.TB, db *sql.DB, repositoryID int) []int {
	query := sqlf.Sprintf(
		`SELECT upload_id FROM lsif_uploads_visible_at_tip WHERE repository_id = %s AND is_default_branch ORDER BY upload_id`,
		repositoryID,
	)

	ids, err := basestore.ScanInts(db.QueryContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...))
	if err != nil {
		t.Fatalf("unexpected error getting uploads visible at tip: %s", err)
	}

	return ids
}

func getProtectedUploads(t testing.TB, db *sql.DB, repositoryID int) []int {
	query := sqlf.Sprintf(
		`SELECT DISTINCT upload_id FROM lsif_uploads_visible_at_tip WHERE repository_id = %s ORDER BY upload_id`,
		repositoryID,
	)

	ids, err := basestore.ScanInts(db.QueryContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...))
	if err != nil {
		t.Fatalf("unexpected error getting protected uploads: %s", err)
	}

	return ids
}

func normalizeVisibleUploads(uploadMetas map[string][]commitgraph.UploadMeta) map[string][]commitgraph.UploadMeta {
	for _, uploads := range uploadMetas {
		sort.Slice(uploads, func(i, j int) bool {
			return uploads[i].UploadID-uploads[j].UploadID < 0
		})
	}

	return uploadMetas
}

func getUploadStates(db dbutil.DB, ids ...int) (map[int]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	q := sqlf.Sprintf(
		`SELECT id, state FROM lsif_uploads WHERE id IN (%s)`,
		sqlf.Join(intsToQueries(ids), ", "),
	)

	return scanStates(db.QueryContext(context.Background(), q.Query(sqlf.PostgresBindVar), q.Args()...))
}

func getIndexStates(db dbutil.DB, ids ...int) (map[int]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	q := sqlf.Sprintf(
		`SELECT id, state FROM lsif_indexes WHERE id IN (%s)`,
		sqlf.Join(intsToQueries(ids), ", "),
	)

	return scanStates(db.QueryContext(context.Background(), q.Query(sqlf.PostgresBindVar), q.Args()...))
}

// scanStates scans pairs of id/states from the return value of `*Store.query`.
func scanStates(rows *sql.Rows, queryErr error) (_ map[int]string, err error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer func() { err = basestore.CloseRows(rows, err) }()

	states := map[int]string{}
	for rows.Next() {
		var id int
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, err
		}

		states[id] = strings.ToLower(state)
	}

	return states, nil
}

func dumpToUpload(expected Dump) Upload {
	return Upload{
		ID:                expected.ID,
		Commit:            expected.Commit,
		Root:              expected.Root,
		UploadedAt:        expected.UploadedAt,
		State:             expected.State,
		FailureMessage:    expected.FailureMessage,
		StartedAt:         expected.StartedAt,
		FinishedAt:        expected.FinishedAt,
		ProcessAfter:      expected.ProcessAfter,
		NumResets:         expected.NumResets,
		NumFailures:       expected.NumFailures,
		RepositoryID:      expected.RepositoryID,
		RepositoryName:    expected.RepositoryName,
		Indexer:           expected.Indexer,
		AssociatedIndexID: expected.AssociatedIndexID,
	}
}
