package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// integrationDSN returns the ClickHouse DSN from the environment,
// or skips the test if SKIFF_INTEGRATION is not set to "1".
func integrationDSN(t *testing.T) string {
	t.Helper()
	if os.Getenv("SKIFF_INTEGRATION") != "1" {
		t.Skip("set SKIFF_INTEGRATION=1 to run integration tests")
	}
	dsn := os.Getenv("CLICKHOUSE_DSN")
	if dsn == "" {
		t.Fatal("CLICKHOUSE_DSN must be set when SKIFF_INTEGRATION=1")
	}
	return dsn
}

// openStore opens a store and registers Close as a cleanup.
func openStore(t *testing.T, dsn string) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// resetDB drops and recreates the skiff database so each test starts clean.
// Only used in tests that need a from-scratch state (e.g. TestMigrate_AppliesFromScratch).
func resetDB(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.conn.Exec(ctx, `DROP DATABASE IF EXISTS skiff`); err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}
	if err := s.conn.Exec(ctx, `CREATE DATABASE skiff`); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
}

// TestMigrate_AppliesFromScratch drops the skiff database, re-creates it, calls
// Migrate, then checks that all expected tables exist and schema_migrations has a row.
func TestMigrate_AppliesFromScratch(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	resetDB(t, s)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	wantTables := []string{
		"schema_migrations",
		"events",
		"package_observations",
		"classifications",
		"builds",
		"cache_publications",
		"ingest_state",
	}
	for _, tbl := range wantTables {
		var count uint64
		err := s.conn.QueryRow(
			ctx,
			`SELECT count() FROM system.tables WHERE database = 'skiff' AND name = ?`, tbl,
		).Scan(&count)
		if err != nil || count == 0 {
			t.Errorf("expected table skiff.%s to exist after Migrate; err=%v count=%d", tbl, err, count)
		}
	}

	var migVersion uint32
	if err := s.conn.QueryRow(
		ctx,
		`SELECT max(version) FROM skiff.schema_migrations FINAL`,
	).Scan(&migVersion); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if migVersion != 1 {
		t.Errorf("expected schema_migrations.version = 1, got %d", migVersion)
	}
}

// TestMigrate_Idempotent calls Migrate twice and confirms the second call is a no-op.
func TestMigrate_Idempotent(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	resetDB(t, s)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// schema_migrations should still have exactly one row for version=1.
	var rowCount uint64
	if err := s.conn.QueryRow(
		ctx,
		`SELECT count() FROM skiff.schema_migrations FINAL WHERE version = 1`,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	// FINAL collapses duplicates by the ReplacingMergeTree key (version),
	// so count should be exactly 1 even if we inserted twice.
	if rowCount != 1 {
		t.Errorf("expected 1 row in schema_migrations after idempotent Migrate, got %d", rowCount)
	}
}

// TestChangesCheckpoint_RoundTrip sets and gets the checkpoint twice.
func TestChangesCheckpoint_RoundTrip(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	// Ensure schema exists.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// First read on a clean ingest_state should return 0.
	// (We truncate the table to isolate from other tests.)
	if err := s.conn.Exec(ctx, `TRUNCATE TABLE skiff.ingest_state`); err != nil {
		t.Fatalf("truncate ingest_state: %v", err)
	}

	got, err := s.GetChangesCheckpoint(ctx)
	if err != nil {
		t.Fatalf("GetChangesCheckpoint (empty): %v", err)
	}
	if got != 0 {
		t.Errorf("expected 0 for empty state, got %d", got)
	}

	if err := s.SetChangesCheckpoint(ctx, 42); err != nil {
		t.Fatalf("SetChangesCheckpoint(42): %v", err)
	}
	got, err = s.GetChangesCheckpoint(ctx)
	if err != nil {
		t.Fatalf("GetChangesCheckpoint after set(42): %v", err)
	}
	if got != 42 {
		t.Errorf("expected 42, got %d", got)
	}

	if err := s.SetChangesCheckpoint(ctx, 9999); err != nil {
		t.Fatalf("SetChangesCheckpoint(9999): %v", err)
	}
	got, err = s.GetChangesCheckpoint(ctx)
	if err != nil {
		t.Fatalf("GetChangesCheckpoint after set(9999): %v", err)
	}
	if got != 9999 {
		t.Errorf("expected 9999, got %d", got)
	}
}

// TestRecordObservation_AndRead inserts an observation and confirms the row exists.
func TestRecordObservation_AndRead(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	obs := Observation{
		ObservedAt:       time.Now().UTC().Truncate(time.Millisecond),
		Name:             "test-pkg-obs-roundtrip",
		Version:          "1.0.0",
		RegistrySeq:      12345,
		PackumentRev:     "1-abc",
		TarballURL:       "https://registry.npmjs.org/test-pkg-obs-roundtrip/-/test-pkg-obs-roundtrip-1.0.0.tgz",
		TarballSHA512Hex: "aabbccdd" + "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" + "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		TarballSizeBytes: 4096,
		SourceObjectKey:  "sources/aabb/test-pkg-obs-roundtrip-1.0.0.tgz",
	}
	// Ensure the sha512 hex is exactly 128 chars.
	if len(obs.TarballSHA512Hex) != 128 {
		// Build a 128-char placeholder.
		h := ""
		for len(h) < 128 {
			h += "a"
		}
		obs.TarballSHA512Hex = h
	}

	if err := s.RecordObservation(ctx, obs); err != nil {
		t.Fatalf("RecordObservation: %v", err)
	}

	var count uint64
	if err := s.conn.QueryRow(
		ctx,
		`SELECT count() FROM skiff.package_observations WHERE name = ? AND version = ?`,
		obs.Name, obs.Version,
	).Scan(&count); err != nil {
		t.Fatalf("count package_observations: %v", err)
	}
	if count == 0 {
		t.Errorf("expected at least one row in package_observations after RecordObservation")
	}
}

// TestRecordClassification_LatestWins inserts two classifications with different
// timestamps and confirms GetLatestClassification returns the newer one.
func TestRecordClassification_LatestWins(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	name := "test-pkg-class-latest-wins"
	version := "2.0.0"

	// Truncate to avoid cross-test interference on this (name, version).
	if err := s.conn.Exec(ctx, `TRUNCATE TABLE skiff.classifications`); err != nil {
		t.Fatalf("truncate classifications: %v", err)
	}

	t1 := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	t2 := time.Now().UTC().Truncate(time.Millisecond)

	c1 := Classification{
		ClassifiedAt:      t1,
		Name:              name,
		Version:           version,
		Classification:    "has_lifecycle_script",
		Reason:            "has install script",
		RuleMatched:       "lifecycle",
		ClassifierVersion: "0.1.0",
	}
	c2 := Classification{
		ClassifiedAt:      t2,
		Name:              name,
		Version:           version,
		Classification:    "pure_js",
		Reason:            "no scripts",
		RuleMatched:       "",
		ClassifierVersion: "0.1.1",
	}

	if err := s.RecordClassification(ctx, c1); err != nil {
		t.Fatalf("RecordClassification v1: %v", err)
	}
	if err := s.RecordClassification(ctx, c2); err != nil {
		t.Fatalf("RecordClassification v2: %v", err)
	}

	// Force ClickHouse to merge (optional; FINAL handles it without merge).
	got, found, err := s.GetLatestClassification(ctx, name, version)
	if err != nil {
		t.Fatalf("GetLatestClassification: %v", err)
	}
	if !found {
		t.Fatal("expected classification to be found")
	}
	// ReplacingMergeTree(classified_at): the row with the highest classified_at wins.
	if got.Classification != "pure_js" {
		t.Errorf("expected latest classification 'pure_js', got %q", got.Classification)
	}
	if got.ClassifierVersion != "0.1.1" {
		t.Errorf("expected classifier_version '0.1.1', got %q", got.ClassifierVersion)
	}
}

// TestRecordBuildSuccess inserts a successful build and reads it back via GetLatestBuild.
func TestRecordBuildSuccess(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	key := BuildKey{
		Name:          "test-pkg-build-success",
		Version:       "3.0.0",
		System:        "x86_64-linux",
		NodejsVersion: "20",
	}

	if err := s.RecordBuildStarted(ctx, key); err != nil {
		t.Fatalf("RecordBuildStarted: %v", err)
	}

	// Build valid 64-char hex strings.
	narHex := "aabbccddeeff00112233445566778899aabbccddeeff001122334455667788"
	for len(narHex) < 64 {
		narHex += "9"
	}
	fileHex := "bbccddeeff00112233445566778899aabbccddeeff0011223344556677889900"
	for len(fileHex) < 64 {
		fileHex += "0"
	}
	narHex = narHex[:64]
	fileHex = fileHex[:64]

	success := BuildSuccess{
		BuildKey:        key,
		StorePath:       "/nix/store/aaaa-test-pkg-build-success-3.0.0",
		NarSHA256Hex:    narHex,
		NarSizeBytes:    1024,
		FileSHA256Hex:   fileHex,
		FileSizeBytes:   512,
		BuildDurationMS: 30000,
	}
	if err := s.RecordBuildSuccess(ctx, success); err != nil {
		t.Fatalf("RecordBuildSuccess: %v", err)
	}

	build, found, err := s.GetLatestBuild(ctx, key.Name, key.Version, key.System, key.NodejsVersion)
	if err != nil {
		t.Fatalf("GetLatestBuild: %v", err)
	}
	if !found {
		t.Fatal("expected build to be found")
	}
	if build.Status != "success" {
		t.Errorf("expected status 'success', got %q", build.Status)
	}
	if build.StorePath != success.StorePath {
		t.Errorf("store_path mismatch: got %q, want %q", build.StorePath, success.StorePath)
	}
	if build.NarSHA256Hex != narHex {
		t.Errorf("nar_sha256_hex mismatch: got %q, want %q", build.NarSHA256Hex, narHex)
	}
}

// TestRecordEvent inserts one row per event_type and checks they round-trip.
func TestRecordEvent(t *testing.T) {
	dsn := integrationDSN(t)
	s := openStore(t, dsn)
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	eventTypes := []string{
		"package_observed",
		"classified",
		"build_started",
		"built",
		"build_failed",
		"cache_published",
		"ingest_checkpoint",
		"workflow_failed",
	}

	for _, et := range eventTypes {
		e := Event{
			EventType:  et,
			Name:       "test-event-pkg",
			Version:    "1.0.0",
			WorkflowID: "wf-" + et,
			Payload:    `{"test":true}`,
		}
		if err := s.RecordEvent(ctx, e); err != nil {
			t.Errorf("RecordEvent(%s): %v", et, err)
		}
	}

	// Verify at least one row per event_type landed.
	for _, et := range eventTypes {
		var count uint64
		if err := s.conn.QueryRow(
			ctx,
			`SELECT count() FROM skiff.events WHERE event_type = ? AND name = 'test-event-pkg'`,
			et,
		).Scan(&count); err != nil {
			t.Errorf("count events for %s: %v", et, err)
			continue
		}
		if count == 0 {
			t.Errorf("expected at least one row in events for event_type=%s", et)
		}
	}
}
