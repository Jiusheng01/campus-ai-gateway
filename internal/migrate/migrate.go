// Package migrate applies the repository's SQL migrations to a PostgreSQL
// database.
//
// Why this exists as its own command rather than a step inside the server:
// the compose file used to mount only 0001_init.sql into PostgreSQL's
// docker-entrypoint-initdb.d. That directory runs once, on an empty volume,
// and never runs the remaining files — so a fresh deployment ended up with the
// initial tables and none of the columns the current code reads. A migration
// runner that owns the whole set, in order, with a record of what it applied,
// is the only way the schema and the binary cannot drift apart.
//
// The contract, in the order it matters:
//
//   - Every file in the directory is applied, in filename order, exactly once.
//   - A file is applied inside its own transaction, and its row in
//     schema_migrations is written in that same transaction: a failed file
//     leaves no record, so re-running retries it and only it.
//   - The checksum of an already-recorded file must match the file on disk.
//     History is not edited in place; a changed historical migration is a
//     deployment error, not something to replay over live data. The digest is
//     taken over the body with line endings normalised, so the same commit
//     yields the same checksum from a Windows checkout and a Linux one: a
//     difference in newline style is not an edit to the migration.
//   - A database that predates the table is replayed from the beginning —
//     the migrations are written to be replay-safe — and the replayed schema
//     is verified against the end-state baseline before anything is recorded,
//     so bringing an old deployment up to date is one command and a drifted
//     database is reported rather than quietly marked as migrated.
//   - Concurrent runs serialise on a PostgreSQL advisory lock, so two
//     containers starting at once cannot interleave.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// schemaMigrationsTable is where applied migrations are recorded. The name is
// fixed: an existing deployment's bookkeeping must not be configurable.
const schemaMigrationsTable = "schema_migrations"

// migrationLockKey serialises concurrent migration runs. The value is
// arbitrary but must never change: it identifies this application's migration
// lock, and reusing another application's key would couple two deployments.
const migrationLockKey int64 = 0x63616d70757301 // "campus" + version tag

// migrationFilePattern is the contract for a migration file name:
// exactly four digits, an underscore, then a lowercase name made of letters,
// digits and underscores, and a lowercase .sql extension.
//
// A file that ends in .sql but does not match this pattern is a deployment
// error, not a silently skipped migration: a mis-named file would otherwise be
// dropped from the history without anyone noticing, and the schema would
// silently diverge from the binary. The four-digit width keeps the ordering
// stable and forces every new file to be padded, so two files can never share
// a numeric prefix by accident of formatting.
var migrationFilePattern = regexp.MustCompile(`^\d{4}_[a-z0-9][a-z0-9_]*\.sql$`)

// Migration is one file in the migrations directory.
type Migration struct {
	// Version is the numeric prefix. It is the ordering key and the primary
	// key of the bookkeeping table.
	Version int
	// Filename is the base name, e.g. "0001_init.sql".
	Filename string
	// Checksum is the hex SHA-256 of the file body with line endings
	// normalised to LF. Normalising is what makes the digest a property of the
	// migration rather than of the checkout that produced it.
	Checksum string
	// Body is the SQL text.
	Body string
}

// Result reports what a run did.
type Result struct {
	// Applied lists the filenames this run applied, in order.
	Applied []string
	// Recorded is the number of migrations recorded after the run.
	Recorded int
	// Bootstrapped is true when the database had no schema_migrations table,
	// so the run replayed every file to bring an existing database under
	// bookkeeping.
	Bootstrapped bool
}

// Errors are sentinel-checkable so the command can map them to distinct exit
// messages without parsing text.
var (
	// ErrNoMigrations means the directory holds no migration files.
	ErrNoMigrations = errors.New("no migration files found")
	// ErrChecksumMismatch means a recorded migration no longer matches the
	// file on disk.
	ErrChecksumMismatch = errors.New("recorded migration checksum does not match the file on disk")
	// ErrMissingMigration means a recorded version is absent from the
	// directory.
	ErrMissingMigration = errors.New("a recorded migration is missing from the migrations directory")
	// ErrFilenameMismatch means a recorded migration no longer carries the
	// filename it was applied under. A rename is an edit of history that keeps
	// the checksum intact, so the name has to be checked too: without it,
	// 0007_a.sql could be renamed to 0007_b.sql and the bookkeeping would
	// still agree.
	ErrFilenameMismatch = errors.New("recorded migration filename does not match the file on disk")
	// ErrVersionGap means a version below the highest recorded one was never
	// recorded, which would mean an out-of-order history.
	ErrVersionGap = errors.New("migration versions are not contiguous")
)

// normalizeNewlines folds CRLF and lone CR to LF.
//
// The repository is checked out with CRLF on Windows and with LF elsewhere,
// because git stores the blob with LF and core.autocrlf rewrites it on the way
// out. SQL attaches no meaning to the difference, so hashing the raw bytes made
// the checksum a property of the platform: the same commit produced two
// digests, and an ordinary `git checkout` became a false "this migration was
// edited" that refused to migrate at all.
func normalizeNewlines(body []byte) []byte {
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] == '\r' {
			// A CRLF pair collapses to the LF that follows it; a lone CR
			// becomes an LF, so both spellings of a line break agree.
			if i+1 < len(body) && body[i+1] == '\n' {
				continue
			}
			out = append(out, '\n')
			continue
		}
		out = append(out, body[i])
	}
	return out
}

// checksumOf is the canonical digest: the body with line endings normalised to
// LF. New rows are recorded in this form.
func checksumOf(body []byte) string {
	sum := sha256.Sum256(normalizeNewlines(body))
	return hex.EncodeToString(sum[:])
}

// crlfChecksumOf is the digest of the CRLF spelling of the same body.
//
// It exists to read rows written before the digest was normalised. Those hold
// the hash of whatever bytes the checkout happened to produce, which on Windows
// was CRLF. Accepting both spellings keeps an existing database migratable
// without rewriting its history, and it still rejects a real edit, because an
// edit changes both digests.
func crlfChecksumOf(body []byte) string {
	lf := normalizeNewlines(body)
	crlf := make([]byte, 0, len(lf)+16)
	for _, b := range lf {
		if b == '\n' {
			crlf = append(crlf, '\r')
		}
		crlf = append(crlf, b)
	}
	sum := sha256.Sum256(crlf)
	return hex.EncodeToString(sum[:])
}

// checksumMatches reports whether a recorded digest describes this body.
func checksumMatches(recorded string, body []byte) bool {
	return recorded == checksumOf(body) || recorded == crlfChecksumOf(body)
}

// Load reads and parses the migrations directory.
//
// Files are sorted by numeric version, and the numeric prefix is the version.
// A file whose name does not carry the prefix is an error rather than a
// silently ordered entry: ordering is the whole safety property here.
func Load(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// A non-.sql file (a README, an editor backup, a stray dump) is not a
		// migration. The comparison is case-insensitive on the extension so a
		// file named with a capital .SQL is still recognised as a SQL file and
		// then judged by the strict pattern below — never silently skipped.
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}
		// A .sql file with a name that does not match the contract is a
		// deployment error, not a silently skipped migration: a mis-named file
		// would otherwise be dropped from the history without anyone noticing,
		// and the schema would silently diverge from the binary.
		if !migrationFilePattern.MatchString(name) {
			return nil, fmt.Errorf("migration file %q has an invalid name: expected %s", name, migrationFilePattern)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, ErrNoMigrations
	}
	sort.Strings(names)

	out := make([]Migration, 0, len(names))
	seen := make(map[int]string, len(names))
	for _, name := range names {
		version := parseVersion(name)
		if prior, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %04d", prior, name, version)
		}
		seen[version] = name
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out = append(out, Migration{
			Version:  version,
			Filename: name,
			Checksum: checksumOf(body),
			Body:     string(body),
		})
	}
	// Order by numeric version, not by raw string, so a hand-named set that is
	// lexically unsorted still applies in the right sequence.
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	// Versions must start at 1 and be contiguous: a hole means someone
	// deleted a historical file, and applying around it would leave the
	// schema unable to say which state it is in.
	for i, m := range out {
		if want := i + 1; m.Version != want {
			return nil, fmt.Errorf("migration versions must be contiguous from 1: %s is version %d, want %d", m.Filename, m.Version, want)
		}
	}
	return out, nil
}

// parseVersion extracts the numeric prefix of a filename already matched by
// migrationFilePattern.
func parseVersion(name string) int {
	n := 0
	for _, r := range name {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// recorded is one row of schema_migrations.
type recorded struct {
	Version  int
	Filename string
	Checksum string
}

// checkHistory compares the bookkeeping table against the files on disk.
//
// It returns the migrations that still need applying. It fails when history
// and disk disagree, because that is the one case where guessing has no safe
// answer: a changed file could mean either "the migration was edited" or "the
// checksum column is wrong", and replaying over live data on a guess is how a
// deployment loses its schema.
//
// A difference in line endings is not a disagreement. The digest is compared
// against the normalised body, and against its CRLF spelling so that rows
// recorded before that normalisation existed stay valid.
func checkHistory(onDisk []Migration, inDB []recorded) (pending []Migration, err error) {
	byVersion := make(map[int]Migration, len(onDisk))
	for _, m := range onDisk {
		byVersion[m.Version] = m
	}

	highest := 0
	for _, r := range inDB {
		file, ok := byVersion[r.Version]
		if !ok {
			return nil, fmt.Errorf("%w: version %d (%s) is recorded but not in the directory", ErrMissingMigration, r.Version, r.Filename)
		}
		if !checksumMatches(r.Checksum, []byte(file.Body)) {
			return nil, fmt.Errorf("%w: %s has checksum %s, recorded %s", ErrChecksumMismatch, file.Filename, file.Checksum, r.Checksum)
		}
		if file.Filename != r.Filename {
			return nil, fmt.Errorf("%w: version %d is recorded as %s, the directory has %s", ErrFilenameMismatch, r.Version, r.Filename, file.Filename)
		}
		if r.Version > highest {
			highest = r.Version
		}
	}

	if len(inDB) > 0 {
		// Every version up to the recorded maximum must be present: a gap
		// means the table was written out of order, and applying the pending
		// set on top of a hole produces a schema nothing has ever tested.
		seen := make(map[int]bool, len(inDB))
		for _, r := range inDB {
			seen[r.Version] = true
		}
		for v := 1; v <= highest; v++ {
			if !seen[v] {
				return nil, fmt.Errorf("%w: version %04d is missing from schema_migrations", ErrVersionGap, v)
			}
		}
	}

	for _, m := range onDisk {
		applied := false
		for _, r := range inDB {
			if r.Version == m.Version {
				applied = true
				break
			}
		}
		if !applied {
			pending = append(pending, m)
		}
	}
	return pending, nil
}

// Apply brings the database at databaseURL up to date with the migrations in
// dir. It is safe to run concurrently and safe to run repeatedly.
func Apply(ctx context.Context, databaseURL, dir string) (Result, error) {
	files, err := Load(dir)
	if err != nil {
		return Result{}, err
	}

	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return Result{}, fmt.Errorf("parse database url: %w", err)
	}
	// The migration files are multi-statement scripts, which the extended
	// query protocol refuses to run in one call. The simple protocol sends
	// them the way psql does.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return Result{}, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// A session-level advisory lock, not a transaction one: it must outlive
	// each migration's own transaction, otherwise the lock would be released
	// halfway through a run on the first COMMIT.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return Result{}, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if err := ensureBookkeeping(ctx, conn); err != nil {
		return Result{}, err
	}

	inDB, err := readRecorded(ctx, conn)
	if err != nil {
		return Result{}, err
	}

	pending, err := checkHistory(files, inDB)
	if err != nil {
		return Result{}, err
	}

	if len(inDB) == 0 {
		// A database with no recorded migration is either on its first run or is
		// retrying a bootstrap whose final verification failed. The bookkeeping
		// table itself may already exist from that failed attempt; zero rows, not
		// table existence, is therefore the bootstrap state. The database is
		// either empty or a legacy deployment whose schema predates this runner.
		// The migrations are replay-safe, so the
		// same replay brings a legacy schema forward, but a legacy database may
		// have drifted in ways replay cannot see: CREATE ... IF NOT EXISTS
		// silently no-ops, ALTER ... ADD COLUMN IF NOT EXISTS skips an existing
		// stub, and a hand-built table passes an existence check.
		//
		// applyBootstrap replays the DDL, verifies the end state against the
		// contract, and only then writes the bookkeeping rows. A drifted
		// database therefore fails closed: the contract check returns before
		// any bookkeeping is written, so schema_migrations stays empty and the
		// deployment never trusts a half-verified schema. The next run retries
		// rather than recording a history the database does not have.
		if err := applyBootstrap(ctx, conn, pending); err != nil {
			return Result{}, err
		}
		names := make([]string, 0, len(pending))
		for _, m := range pending {
			names = append(names, m.Filename)
		}
		return Result{Applied: names, Recorded: len(files), Bootstrapped: true}, nil
	}

	result := Result{}
	for _, m := range pending {
		if err := applyOne(ctx, conn, m); err != nil {
			return Result{}, err
		}
		result.Applied = append(result.Applied, m.Filename)
	}
	// The migration gate is also a drift gate. A database with recorded history
	// can still be damaged or hand-edited after the records were written; only
	// the final catalog proves the binary can safely use it. Verification on
	// every run also means an empty bookkeeping table left by a prior failed
	// bootstrap can never turn the next invocation into an unchecked success.
	if err := verifyFinalSchema(ctx, conn); err != nil {
		return Result{}, err
	}

	result.Recorded = len(files)
	return result, nil
}

// ensureBookkeeping creates the record table if the database has never seen
// this runner.
func ensureBookkeeping(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+schemaMigrationsTable+` (
		version    INTEGER PRIMARY KEY,
		filename   VARCHAR(255) NOT NULL,
		checksum   VARCHAR(64)  NOT NULL,
		applied_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		return fmt.Errorf("create %s: %w", schemaMigrationsTable, err)
	}
	return nil
}

// readRecorded loads the bookkeeping table.
func readRecorded(ctx context.Context, conn *pgx.Conn) ([]recorded, error) {
	rows, err := conn.Query(ctx,
		`SELECT version, filename, checksum FROM `+schemaMigrationsTable+` ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", schemaMigrationsTable, err)
	}
	defer rows.Close()

	var out []recorded
	for rows.Next() {
		var r recorded
		if err := rows.Scan(&r.Version, &r.Filename, &r.Checksum); err != nil {
			return nil, fmt.Errorf("scan %s: %w", schemaMigrationsTable, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", schemaMigrationsTable, err)
	}
	return out, nil
}

// applyOne runs a migration and records it in the same transaction.
//
// Same transaction is the point: a file that half-applies takes its
// bookkeeping row down with it, so the next run sees it as pending rather than
// as applied, and a partially migrated schema never becomes the recorded
// truth.
func applyOne(ctx context.Context, conn *pgx.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", m.Filename, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, m.Body); err != nil {
		return fmt.Errorf("apply %s: %w", m.Filename, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO `+schemaMigrationsTable+` (version, filename, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Filename, m.Checksum); err != nil {
		return fmt.Errorf("record %s: %w", m.Filename, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", m.Filename, err)
	}
	return nil
}

// applyBootstrap replays every migration, verifies the end-state contract, and
// then records the bookkeeping rows.
//
// The replay and the verification cannot live in a single transaction under the
// pgx simple query protocol: a multi-statement migration body plus the catalog
// introspection that verifyFinalSchema runs do not survive one open
// transaction (the connection reports the transaction closed before COMMIT).
// The bootstrap is therefore phased, and the safety property is preserved by
// ordering rather than by atomicity:
//
//  1. applyWithoutRecording replays each migration in its own committed
//     transaction. The migrations are replay-safe (CREATE ... IF NOT EXISTS,
//     ALTER ... ADD COLUMN IF NOT EXISTS), so against a database that already
//     has the objects this is a no-op, and against an empty one it builds the
//     full schema.
//  2. verifyFinalSchema reads the catalog from the bare connection and fails
//     closed when any table, column, type, nullability, unique constraint,
//     foreign key or index is missing or drifted — naming every gap.
//  3. recordAll writes the bookkeeping rows for the whole set in one
//     transaction, only after verification passed.
//
// Fail-closed is preserved even though step 1 commits: step 1 only replays
// idempotent DDL, so a drifted database is left exactly as it was — the
// existing objects no-op, verification still fails, and no bookkeeping row is
// ever written. The deployment therefore never trusts a half-verified schema,
// and the next run retries rather than recording a history the database does
// not have.
func applyBootstrap(ctx context.Context, conn *pgx.Conn, files []Migration) error {
	if err := applyWithoutRecording(ctx, conn, files); err != nil {
		return err
	}
	if err := verifyFinalSchema(ctx, conn); err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := recordAll(ctx, tx, files); err != nil {
		return fmt.Errorf("record bootstrap: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bootstrap: %w", err)
	}
	return nil
}

// applyWithoutRecording replays every migration in its own committed
// transaction, without writing any bookkeeping. It is the replay half of the
// bootstrap: the migrations are replay-safe, so this is safe to run against a
// database that already has some or all of the objects.
func applyWithoutRecording(ctx context.Context, conn *pgx.Conn, files []Migration) error {
	for _, m := range files {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin %s: %w", m.Filename, err)
		}
		if _, err := tx.Exec(ctx, m.Body); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", m.Filename, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", m.Filename, err)
		}
	}
	return nil
}

// recordAll writes the bookkeeping rows for the whole bootstrap in one
// transaction. It does not commit: the caller owns the transaction so the
// bootstrap can commit once after verification has passed.
//
// One transaction, not one per file, because the bootstrap is one decision:
// after it, the database claims the full set as applied. A partial row set
// would describe a history that never happened.
func recordAll(ctx context.Context, tx pgx.Tx, files []Migration) error {
	for _, m := range files {
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+schemaMigrationsTable+` (version, filename, checksum) VALUES ($1, $2, $3)`,
			m.Version, m.Filename, m.Checksum); err != nil {
			return fmt.Errorf("record %s: %w", m.Filename, err)
		}
	}
	return nil
}
