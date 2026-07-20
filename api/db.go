package api

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

// db is nil unless DATABASE_URL is set - every function in this file is a
// deliberate no-op against a nil db, so a node with no Postgres configured
// behaves exactly as it always has (state.json remains the sole source of
// truth). This is the dual-write phase of the Postgres migration: state.json
// stays authoritative for reads (LoadState in persistence.go is unchanged),
// Postgres is written to as a second, durable copy - never the other way
// around yet. See audit/KNOWN_GAPS.md's "single-JSON-file persistence" entry
// for why this migration exists, and the production-readiness plan's Phase 3
// note on why a hard cutover on day one would be the wrong move: a bug in
// the new path should never be able to silently lose data before it's had
// time to prove itself running alongside the known-good path.
var db *sql.DB

// dbSnapshotRetention is how many historical snapshots to keep per node
// before pruning older ones - bounds table growth the same way state.json's
// wholesale-rewrite-every-save pattern doesn't. Keeping more than one is
// deliberate: a single "latest only" row would inherit state.json's own
// disclosed risk (one bad write clobbers the only copy) rather than fixing
// it.
const dbSnapshotRetention = 20

// nodeID identifies which node's snapshots are whose in a shared database -
// distinct from core's node-identity keypair (core/node_identity.go), this
// is just a label. Falls back to the hostname so a single-node local run
// still gets a stable, meaningful value without any extra configuration.
func nodeID() string {
	if v := os.Getenv("NODE_ID"); v != "" {
		return v
	}
	if v := os.Getenv("SELF_URL"); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil {
		return "unknown-node"
	}
	return host
}

// InitDB connects to Postgres if DATABASE_URL is set and ensures the
// snapshot table exists. Deliberately non-fatal on any failure (missing env
// var, unreachable database, bad credentials) - this is a durability
// upgrade layered on top of an already-working persistence mechanism
// (state.json), not a new hard dependency the node can't start without.
// A node operator who hasn't provisioned Postgres yet should see a clear
// log line, not a crash.
func InitDB() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Println("DATABASE_URL not set - running on state.json only (no Postgres dual-write)")
		return
	}

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Println("Postgres dual-write disabled: failed to open connection:", err)
		return
	}
	if err := conn.Ping(); err != nil {
		log.Println("Postgres dual-write disabled: failed to reach database:", err)
		conn.Close()
		return
	}

	if _, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS state_snapshots (
			id         BIGSERIAL PRIMARY KEY,
			node_id    TEXT NOT NULL,
			snapshot   JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_state_snapshots_node_created
			ON state_snapshots (node_id, created_at DESC);
	`); err != nil {
		log.Println("Postgres dual-write disabled: failed to create schema:", err)
		conn.Close()
		return
	}

	db = conn
	log.Println("Postgres dual-write enabled for node:", nodeID())
}

// saveSnapshotToDB writes the same JSON blob SaveState just wrote to
// state.json into Postgres as well, then prunes old snapshots for this
// node beyond dbSnapshotRetention. Called from SaveState in persistence.go;
// any error here is logged, never returned/propagated - a Postgres hiccup
// must never block the state.json write path that's worked all along.
func saveSnapshotToDB(data []byte) {
	if db == nil {
		return
	}

	if _, err := db.Exec(
		`INSERT INTO state_snapshots (node_id, snapshot) VALUES ($1, $2)`,
		nodeID(), data,
	); err != nil {
		log.Println("Postgres snapshot write failed (state.json write is unaffected):", err)
		return
	}

	if _, err := db.Exec(`
		DELETE FROM state_snapshots
		WHERE node_id = $1
		AND id NOT IN (
			SELECT id FROM state_snapshots
			WHERE node_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		)
	`, nodeID(), dbSnapshotRetention); err != nil {
		log.Println("Postgres snapshot prune failed (not fatal, table just grows a bit more):", err)
	}
}

// latestSnapshotFromDB fetches this node's most recent Postgres snapshot -
// meant for disaster recovery (restoring onto a fresh disk that lost
// state.json entirely), not for normal startup, which still reads
// state.json via LoadState. Exposed so an operator/ops script can pull the
// latest snapshot back down without needing direct SQL access.
func latestSnapshotFromDB() ([]byte, error) {
	if db == nil {
		return nil, fmt.Errorf("no database configured (DATABASE_URL not set)")
	}

	var data []byte
	err := db.QueryRow(
		`SELECT snapshot FROM state_snapshots WHERE node_id = $1 ORDER BY created_at DESC LIMIT 1`,
		nodeID(),
	).Scan(&data)
	if err != nil {
		return nil, err
	}
	return data, nil
}
