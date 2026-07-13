# Postgres dual-write setup (Phase 3)

This is the operator-facing setup guide for the Postgres dual-write layer
added in `api/db.go`. It's a **dual-write**, not a cutover: `state.json`
remains the sole source of truth a node actually loads from at startup
(`LoadState()` is untouched). Every save additionally writes the same JSON
snapshot into Postgres, so there's a durable, off-disk copy independent of
the VM's own storage - directly addressing the "single JSON file, unbounded
growth, single-write-can-corrupt-everything" gap in `audit/KNOWN_GAPS.md`.

**Nothing changes if you don't do this setup.** `DATABASE_URL` unset (the
default) means the node behaves exactly as it always has - `InitDB()` logs
one line and returns, every other function in `db.go` is a no-op.

## 1. Provision the database

Any Postgres 13+ instance works. The recommended path (matches the rest of
this project's AWS-based infrastructure) is a small AWS RDS Postgres
instance:

1. AWS Console -> RDS -> Create database.
2. Engine: PostgreSQL. Template: **Free tier** is enough to start (this is
   one small table storing periodic JSON snapshots, not a high-throughput
   OLTP workload).
3. DB instance identifier: `creditchain-state` (or whatever you prefer).
4. Master username/password: generate and store these securely - this
   password is a real secret, handle it the same way the S3 backup
   credentials were handled (write-to-file, never paste into a shell
   command directly).
5. **Connectivity**: place it in the same VPC as your Lightsail
   instances if possible; otherwise you'll need "Publicly accessible: Yes"
   plus a security group rule allowing inbound port 5432 **only from your
   node IPs** (18.130.100.236/32 and 13.40.131.231/32), never 0.0.0.0/0.
6. Note the endpoint hostname RDS gives you once created (looks like
   `creditchain-state.xxxxxxxxxx.eu-west-1.rds.amazonaws.com`).

## 2. Set `DATABASE_URL` on each node

Format: `postgres://USERNAME:PASSWORD@HOST:5432/DBNAME?sslmode=require`

Add this to each node's systemd service environment (`/etc/systemd/system/creditchain.service`
on node1, same file on node2 - see each node's actual `ExecStart`/
`Environment=` lines, which differ slightly between the two per
`ARCHITECTURE.md`). Follow the same credential-handling pattern used for
the S3 backup secret: never paste the password directly into an SSH
command - write the env file locally, `scp` it over, then `sudo mv` it
into place.

```
Environment=DATABASE_URL=postgres://USERNAME:PASSWORD@HOST:5432/creditchain?sslmode=require
Environment=NODE_ID=node1
```

(`NODE_ID` is optional - defaults to the hostname if unset. Set it
explicitly so node1/node2's snapshots are unambiguously labeled in the
shared table, especially if both point at the same RDS instance.)

Then:
```
sudo systemctl daemon-reload
sudo systemctl restart creditchain.service
sudo journalctl -u creditchain.service --no-pager -n 10
```

You should see `Postgres dual-write enabled for node: node1` (or whatever
`NODE_ID`/hostname resolves to) in the log. If you instead see a "disabled"
line, the log message itself says why (bad DSN, unreachable host, auth
failure, etc.) - fix that before assuming it's working.

## 3. Verify it's actually writing

Trigger any mutating action (or just wait - most pages poll/refresh, and
any POST request calls `SaveState()`), then check from `psql` or any
Postgres client:

```sql
SELECT node_id, created_at, octet_length(snapshot::text) AS bytes
FROM state_snapshots
ORDER BY created_at DESC
LIMIT 5;
```

You should see recent rows for both `node1` and `node2` (or whatever
`NODE_ID` values you set), growing at roughly the same rate `state.json`
itself changes on each box.

## 4. Restore path (disaster recovery only)

`api/db.go`'s `latestSnapshotFromDB()` fetches the most recent snapshot for
the current node - this is meant for the scenario where a node's disk is
lost entirely and `state.json` no longer exists, **not** for normal
startup (`LoadState()` still reads `state.json` first, unchanged). There is
currently no CLI wired up to invoke this directly; the fastest path today
is:

```sql
-- from any psql session connected to the RDS instance
\copy (SELECT snapshot FROM state_snapshots WHERE node_id = 'node1' ORDER BY created_at DESC LIMIT 1) TO 'recovered_state.json'
```

then place `recovered_state.json` as `state.json` in the node's `DATA_DIR`
before starting `creditchaind`. A proper `chaindiff`-style CLI tool for
this (matching `cmd/chaindiff`'s existing pattern) is a reasonable
follow-up, not yet built.

## 5. What this phase deliberately does NOT do yet

- **No relational schema.** This stores the exact same wholesale JSON blob
  `state.json` already contains, once per save, pruned to the last 20
  snapshots per node - it is a durability upgrade, not a data-model
  upgrade. Decomposing individual stores (wallets, transactions, etc.) into
  real relational tables with proper indexes/constraints is a larger,
  separate follow-up, deliberately not attempted in one pass per the
  original plan's "never a hard cutover on day one" principle.
- **Reads still come from `state.json`, never from Postgres**, at normal
  startup. Postgres is currently write-only from the application's
  perspective (aside from the disaster-recovery path in §4).
- **Not yet tested against a live Postgres instance** as of this write-up -
  the code compiles and passes `go vet`, and the SQL has been hand-reviewed,
  but no local Postgres/Docker was available in the dev environment this
  was written in to run a live integration test. **Treat this as
  unverified until step 3 above has actually been run once against a real
  database.**
