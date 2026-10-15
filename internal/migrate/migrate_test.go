package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- unit tests: no database needed ----------------------------------------

func writeMigration(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadOrdersByVersionAndChecksumsBody(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0002_beta.sql", "SELECT 2;")
	writeMigration(t, dir, "0001_alpha.sql", "SELECT 1;")
	// Not a migration file: ignored rather than a hard error, so a README or
	// an editor backup in the directory cannot break a deployment.
	writeMigration(t, dir, "notes.txt", "not a migration")
	if err := os.Mkdir(filepath.Join(dir, "subdir.sql"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(got))
	}
	if got[0].Version != 1 || got[0].Filename != "0001_alpha.sql" {
		t.Fatalf("expected 0001 first, got %+v", got[0])
	}
	if got[1].Version != 2 || got[1].Filename != "0002_beta.sql" {
		t.Fatalf("expected 0002 second, got %+v", got[1])
	}
	for _, m := range got {
		sum := sha256.Sum256([]byte(m.Body))
		if want := hex.EncodeToString(sum[:]); m.Checksum != want {
			t.Fatalf("%s checksum = %s, want %s", m.Filename, m.Checksum, want)
		}
	}
}

func TestLoadRejectsNonContiguousVersions(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.sql", "SELECT 1;")
	// A hole means a historical file was deleted; applying around it would
	// leave the schema unable to say which state it is in.
	writeMigration(t, dir, "0003_c.sql", "SELECT 3;")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an error for a version gap")
	}
}

func TestLoadEmptyDirectory(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNoMigrations) {
		t.Fatalf("expected ErrNoMigrations, got %v", err)
	}
}

// P1-02: a .sql file whose name does not satisfy the strict pattern is a hard
// error, not a silently ignored migration. Each case below would previously be
// dropped from the history, letting the schema drift from the binary without
// any signal.
func TestLoadRejectsInvalidSQLFileName(t *testing.T) {
	cases := []string{
		"1_init.sql",        // too few digits
		"000001_init.sql",   // too many digits
		"0001_Init.sql",     // uppercase in name
		"0001-init.sql",     // dash instead of underscore
		"0001_init (1).sql", // spaces and parens
		"0001_init.SQL",     // uppercase extension
		"init.sql",          // no version prefix
		"0001.sql",          // no name component
		"0001__init.sql",    // name starts with an underscore
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			// One valid file so the directory is not empty: ErrNoMigrations is
			// a different error and not what this test exercises.
			writeMigration(t, dir, "0001_alpha.sql", "SELECT 1;")
			writeMigration(t, dir, bad, "SELECT 1;")
			if _, err := Load(dir); err == nil {
				t.Fatalf("expected an error for invalid file name %q", bad)
			}
		})
	}
}

// P1-02: a name that starts with an underscore is rejected, because it is too
// easy to mistype the separator and a silent skip is the dangerous outcome.
func TestLoadRejectsLeadingUnderscoreName(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_alpha.sql", "SELECT 1;")
	writeMigration(t, dir, "0002__beta.sql", "SELECT 2;")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an error for a name starting with an underscore")
	}
}

// P1-02: non-.sql files are still ignored, so a README or an editor backup left
// in the directory cannot break a deployment.
func TestLoadIgnoresNonSQLFiles(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_alpha.sql", "SELECT 1;")
	writeMigration(t, dir, "0002_beta.sql", "SELECT 2;")
	writeMigration(t, dir, "README.md", "# not a migration")
	writeMigration(t, dir, "notes.txt", "not a migration")
	if err := os.Mkdir(filepath.Join(dir, "0003_gamma.sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(got))
	}
}

// P1-02: ordering is by numeric version, so a directory listed out of order
// still applies in sequence rather than in lexical order.
func TestLoadSortsByNumericVersion(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0003_gamma.sql", "SELECT 3;")
	writeMigration(t, dir, "0001_alpha.sql", "SELECT 1;")
	writeMigration(t, dir, "0002_beta.sql", "SELECT 2;")
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("expected %d migrations, got %d", len(want), len(got))
	}
	for i, v := range want {
		if got[i].Version != v {
			t.Fatalf("migration %d is version %d, want %d", i, got[i].Version, v)
		}
	}
}

func TestCheckHistorySelectsPending(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.sql", "SELECT 1;")
	writeMigration(t, dir, "0002_b.sql", "SELECT 2;")
	writeMigration(t, dir, "0003_c.sql", "SELECT 3;")
	onDisk, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	pending, err := checkHistory(onDisk, []recorded{
		{Version: 1, Filename: "0001_a.sql", Checksum: onDisk[0].Checksum},
		{Version: 2, Filename: "0002_b.sql", Checksum: onDisk[1].Checksum},
	})
	if err != nil {
		t.Fatalf("checkHistory: %v", err)
	}
	if len(pending) != 1 || pending[0].Version != 3 {
		t.Fatalf("expected only 0003 pending, got %+v", pending)
	}
}

// A recorded file whose bytes changed must stop the run. Replaying a modified
// historical migration over live data is how a deployment loses its schema,
// so this is an error and never a warning.
func TestCheckHistoryDetectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.sql", "SELECT 1;")
	onDisk, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = checkHistory(onDisk, []recorded{
		{Version: 1, Filename: "0001_a.sql", Checksum: strings.Repeat("0", 64)},
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}

// P2-3 (review). A rename keeps the checksum intact, so comparing versions
// and checksums alone would accept an edited history: the file's name is part
// of what was recorded, and it has to be checked too.
func TestCheckHistoryDetectsFilenameMismatch(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.sql", "SELECT 1;")
	onDisk, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = checkHistory(onDisk, []recorded{
		{Version: 1, Filename: "0001_renamed.sql", Checksum: onDisk[0].Checksum},
	})
	if !errors.Is(err, ErrFilenameMismatch) {
		t.Fatalf("expected ErrFilenameMismatch, got %v", err)
	}
}

func TestCheckHistoryDetectsMissingAndGap(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.sql", "SELECT 1;")
	writeMigration(t, dir, "0002_b.sql", "SELECT 2;")
	onDisk, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := checkHistory(onDisk, []recorded{
		{Version: 1, Filename: "0001_a.sql", Checksum: onDisk[0].Checksum},
		{Version: 3, Filename: "0003_c.sql", Checksum: onDisk[1].Checksum},
	}); !errors.Is(err, ErrMissingMigration) {
		t.Fatalf("expected ErrMissingMigration, got %v", err)
	}

	// Version 2 recorded as version 1's checksum makes the table internally
	// inconsistent rather than merely stale.
	if _, err := checkHistory(onDisk[:1], []recorded{
		{Version: 1, Filename: "0001_a.sql", Checksum: onDisk[0].Checksum},
		{Version: 2, Filename: "0002_b.sql", Checksum: onDisk[0].Checksum},
	}); !errors.Is(err, ErrMissingMigration) {
		t.Fatalf("expected ErrMissingMigration, got %v", err)
	}
}

// --- integration tests: isolated database -----------------------------------
//
// §9.2 of the handoff: stateful tests run only against a database created for
// this purpose, named with a phase marker and a random suffix, and dropped on
// the way out. Nothing outside that name is ever touched, and the name is
// printed before it is used. When no PostgreSQL answers, the tests skip —
// they never fall back to a shared database.

const defaultTestDatabaseURL = "postgres://campus:campus@127.0.0.1:5432/postgres?sslmode=disable"

// isolatedDatabase returns a connection string for a fresh, empty database
// owned by this test, and arranges for its removal.
func isolatedDatabase(t *testing.T) string {
	t.Helper()

	base := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if base == "" {
		base = defaultTestDatabaseURL
	}
	cfg, err := pgx.ParseConfig(base)
	if err != nil {
		t.Skipf("cannot parse TEST_DATABASE_URL: %v", err)
	}

	// §9.2: the temporary name carries the phase marker and a random suffix,
	// and a business database name is refused outright.
	suffix := fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	name := fmt.Sprintf("campus_migrate_phase1_%s", suffix[len(suffix)-6:])
	if name == "campus_gateway" || name == "campus_gateway_test" {
		t.Fatalf("refusing to use a business database name: %s", name)
	}
	t.Logf("migrate integration test target: %s on %s", name, cfg.Host)

	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Skipf("test PostgreSQL unavailable: %v (set TEST_DATABASE_URL to run the migration tests)", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	drop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// WITH (FORCE) drops connections a previous run left behind. Only the
		// database this test created is ever named here.
		_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(name)+` WITH (FORCE)`)
	}
	drop()
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+quoteIdent(name)); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(drop)

	out := cfg.Copy()
	out.Database = name
	return stdlibURL(out)
}

// stdlibURL renders a pgx config the way the runner receives it, so the test
// exercises the same pgx.ParseConfig path as production.
func stdlibURL(cfg *pgx.ConnConfig) string {
	query := ""
	if len(cfg.RuntimeParams) > 0 {
		first := true
		for k, v := range cfg.RuntimeParams {
			sep := "&"
			if first {
				sep = "?"
				first = false
			}
			query += sep + k + "=" + v
		}
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s%s",
		cfg.User, cfg.Password, net_JoinHostPort(cfg.Host, cfg.Port), cfg.Database, query)
}

func net_JoinHostPort(host string, port uint16) string {
	return fmt.Sprintf("%s:%d", host, port)
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// repoMigrations is the migrations directory of the repository under test.
const repoMigrations = "../../migrations"

// connect is a convenience for test-side connections.
func connect(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// An empty database receives every migration, in order, and the second run is
// a no-op that changes no business data.
func TestApplyToEmptyDatabaseRecordsEverything(t *testing.T) {
	url := isolatedDatabase(t)

	first, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	want := len(listMigrationFiles(t, repoMigrations))
	if len(first.Applied) != want {
		t.Fatalf("applied %d migrations, want %d: %v", len(first.Applied), want, first.Applied)
	}
	if first.Recorded != want {
		t.Fatalf("recorded %d, want %d", first.Recorded, want)
	}

	conn := connect(t, url)
	rows, err := conn.Query(context.Background(), `SELECT version, filename, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var version int
		var filename, checksum string
		if err := rows.Scan(&version, &filename, &checksum); err != nil {
			t.Fatal(err)
		}
		n++
		if len(checksum) != 64 {
			t.Fatalf("checksum for %s is not a sha256 hex digest: %q", filename, checksum)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("schema_migrations holds %d rows, want %d", n, want)
	}

	// A business table from the migrations exists, so the run really ran.
	var exists bool
	if err := conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'gateway_tenant')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("gateway_tenant must exist after migrating")
	}

	// Second run: nothing applied, nothing recorded twice.
	second, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second apply must be a no-op, applied %v", second.Applied)
	}
	if second.Bootstrapped {
		t.Fatal("a database under bookkeeping must not report a bootstrap")
	}
}

// A recorded file whose bytes changed stops the run instead of replaying.
func TestApplyRejectsChecksumChange(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}

	// A copy of the directory with one historical file edited — the real
	// migrations directory is never modified.
	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(repoMigrations, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == "0002_management.sql" {
			body = append(body, []byte("\n-- tampered for the checksum test\n")...)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Apply(context.Background(), url, dir); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
}

// A file that fails SQL leaves no record of itself, and the run exits with an
// error — so the compose gate keeps the gateway from starting on top of a
// half-migrated schema. This covers the normal path, where the database is
// already under bookkeeping: the applied files keep their rows, the failed one
// gets none.
func TestApplyRecordsNothingForAFailedFile(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}

	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(repoMigrations, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMigration(t, dir, "0010_broken.sql", "INSERT INTO gateway_no_such_table VALUES (1);")

	_, err = Apply(context.Background(), url, dir)
	if err == nil {
		t.Fatal("expected the broken migration to fail the run")
	}

	conn := connect(t, url)
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(listMigrationFiles(t, repoMigrations)) {
		t.Fatalf("the applied set must keep its rows, got %d", count)
	}
	var broken int
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM schema_migrations WHERE filename = '0010_broken.sql'`).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != 0 {
		t.Fatal("the failed version must have no bookkeeping row")
	}
}

// The same failure on the bootstrap path records nothing at all: the run is
// one decision, and a half-verified history is worse than none.
func TestBootstrapRecordsNothingWhenAFileFails(t *testing.T) {
	url := isolatedDatabase(t)
	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(repoMigrations, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMigration(t, dir, "0010_broken.sql", "INSERT INTO gateway_no_such_table VALUES (1);")

	if _, err := Apply(context.Background(), url, dir); err == nil {
		t.Fatal("expected the broken migration to fail the run")
	}

	conn := connect(t, url)
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a failed bootstrap must record nothing, got %d rows", count)
	}
}

// A database that predates the bookkeeping table — the shape every existing
// deployment is in — is brought under bookkeeping by replaying the
// replay-safe migrations and recording them, after the replayed schema is
// verified against the end-state baseline.
func TestApplyBootstrapsLegacyDatabase(t *testing.T) {
	url := isolatedDatabase(t)

	// Build the legacy shape: only 0001, applied directly, with no
	// bookkeeping table at all.
	conn := connect(t, url)
	first := filepath.Join(repoMigrations, "0001_init.sql")
	body, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), string(body)); err != nil {
		t.Fatalf("apply legacy 0001: %v", err)
	}
	_ = conn.Close(context.Background())

	result, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("bootstrap apply: %v", err)
	}
	if !result.Bootstrapped {
		t.Fatal("a database without bookkeeping must report a bootstrap")
	}
	if result.Recorded != len(listMigrationFiles(t, repoMigrations)) {
		t.Fatalf("recorded %d, want %d", result.Recorded, len(listMigrationFiles(t, repoMigrations)))
	}

	// A column added by a later migration exists, proving the replay ran all
	// the way forward rather than stopping at the legacy state.
	conn = connect(t, url)
	var exists bool
	if err := conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'gateway_admin_session' AND column_name = 'previous_refresh_hash')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("the bootstrap must apply the later migrations too")
	}
}

// applyFilesRaw applies the first n migration files directly, with no
// bookkeeping, which is how a database that predates this runner looks.
func applyFilesRaw(t *testing.T, url, dir string, n int) {
	t.Helper()
	conn := connect(t, url)
	defer func() { _ = conn.Close(context.Background()) }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if n > len(names) {
		t.Fatalf("asked for %d files, only %d present", n, len(names))
	}
	for _, name := range names[:n] {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(context.Background(), string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

// A legacy database stopped mid-history — some migrations applied by hand, the
// rest never — is brought forward and recorded, exactly like the 0001-only
// case. The bootstrap replays the whole set either way.
func TestApplyBootstrapsMidHistoryLegacyDatabase(t *testing.T) {
	url := isolatedDatabase(t)
	applyFilesRaw(t, url, repoMigrations, 5)

	result, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("bootstrap apply: %v", err)
	}
	if !result.Bootstrapped {
		t.Fatal("a mid-history legacy database must report a bootstrap")
	}
	if result.Recorded != len(listMigrationFiles(t, repoMigrations)) {
		t.Fatalf("recorded %d, want %d", result.Recorded, len(listMigrationFiles(t, repoMigrations)))
	}
}

// P1-03 (review). A drifted legacy database must fail closed.
//
// The replay cannot see a table that exists with the wrong shape — CREATE
// TABLE IF NOT EXISTS simply no-ops, and the later ALTERs succeed against the
// stub — so without the baseline check the bootstrap would record the whole
// set as applied on a schema that is missing the columns the runtime reads.
// Here gateway_tenant exists with only a primary key: every replay statement
// succeeds, the end-state baseline does not, and the run must refuse rather
// than record a history the database does not have.
func TestApplyFailsClosedOnDriftedLegacyDatabase(t *testing.T) {
	url := isolatedDatabase(t)

	conn := connect(t, url)
	if _, err := conn.Exec(context.Background(),
		`CREATE TABLE gateway_tenant (id UUID PRIMARY KEY)`); err != nil {
		t.Fatalf("create drifted table: %v", err)
	}
	_ = conn.Close(context.Background())

	_, err := Apply(context.Background(), url, repoMigrations)
	if err == nil {
		t.Fatal("a drifted legacy database must fail closed, not record a history it does not have")
	}
	if !strings.Contains(err.Error(), "contract") {
		t.Fatalf("the failure must name the schema contract check, got: %v", err)
	}
	if !strings.Contains(err.Error(), "gateway_tenant.code") {
		t.Fatalf("the failure must name the missing object, got: %v", err)
	}

	// Nothing recorded: the deployment cannot be told the schema is current.
	conn = connect(t, url)
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a failed bootstrap must record nothing, got %d rows", count)
	}
}

// Two concurrent runs serialise on the advisory lock: exactly one applies,
// both finish successfully, and no migration is applied twice.
func TestApplySerialisesConcurrentRuns(t *testing.T) {
	url := isolatedDatabase(t)

	type outcome struct {
		result Result
		err    error
	}
	results := make([]outcome, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := Apply(context.Background(), url, repoMigrations)
			results[i] = outcome{r, err}
		}(i)
	}
	wg.Wait()

	applied := map[string]int{}
	for i, o := range results {
		if o.err != nil {
			t.Fatalf("run %d failed: %v", i, o.err)
		}
		for _, name := range o.result.Applied {
			applied[name]++
		}
	}
	total := len(listMigrationFiles(t, repoMigrations))
	if len(applied) != total {
		t.Fatalf("%d distinct migrations applied, want %d: %v", len(applied), total, applied)
	}
	for name, n := range applied {
		if n != 1 {
			t.Fatalf("%s applied %d times", name, n)
		}
	}
}

func listMigrationFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			out = append(out, e.Name())
		}
	}
	return out
}
