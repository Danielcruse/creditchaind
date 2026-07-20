# Running your own CreditChain node

A node is a separate process from the website - `creditchaind` (this
doc) vs. the static website (`cmd/staticserve`, only needed if you
also want to host a copy of the site). Running a node means
participating in consensus/gossip, not serving web pages.

**This network is currently permissioned, not open/permissionless.**
Anyone can build and run the binary, but joining the actual gossip
network requires the operator (whoever runs the existing nodes) to
hand you a shared secret and add your node's address to their peer
list. There's no self-serve signup flow yet - see the "Known
limitations" section at the bottom.

## What you need before starting

1. **The `NETWORK_SECRET` value** - ask the network operator for this.
   Every node must present it on peer-to-peer requests; a node without
   it can't gossip transactions or blocks with anyone.
2. **A publicly reachable host:port** for your own node, so other
   nodes can reach you back (`SELF_URL` below).
3. **Confirmation from the operator** that they've added your
   `SELF_URL` to their own `PEERS` list (this is currently a manual,
   two-way step - see limitations).

## Environment variables

| Variable | Required | Meaning |
|---|---|---|
| `PORT` | no (default `8080`) | Port this node listens on. |
| `DATA_DIR` | no (default `.`) | Where `state.json`/`blockchain.json`/`identity.json` are read/written - gives each node process its own isolated data. |
| `PEERS` | yes | Comma-separated list of other nodes' base URLs (e.g. `http://node1.example.com:8080,http://node2.example.com:8080`) this node dials on startup to join the network. |
| `NETWORK_SECRET` | yes | Shared secret required on every peer-to-peer request - get this from the network operator. |
| `SELF_URL` | yes | This node's own publicly reachable base URL, so peers can register you back and route gossip to you. |
| `ADMIN_WALLET_ADDRESS` | no (but must match network-wide once set) | The ordinary CreditChain wallet address (created via `register.html`, no special key type) authorized to sign admin actions (mint, treasury controls, genesis-pool allocation/withdrawal, admin transfers, announcements) - see `api/admin.go`'s `requireAdminAuthed`/`verifyAdminTxAuth`. Every node applying an admin transaction independently checks its signature against this value, so it must be set identically on every node, unlike `ADMIN_TOKEN` below which is checked only by whichever node happens to receive the HTTP request. If unset, falls back to the legacy `ADMIN_TOKEN` model. |
| `ADMIN_TOKEN` | no | Legacy admin auth - a shared bearer secret checked via the `X-Admin-Token` header. Only takes effect when `ADMIN_WALLET_ADDRESS` is NOT set; once an admin wallet is configured, this stops working entirely (by design - see `requireAdminAuthed`'s doc comment). Kept only for networks that haven't migrated to the signed-wallet model yet. |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | no | If both are set, serves HTTPS directly. If unset, serves plain HTTP - fine behind a reverse proxy (nginx/Caddy) that terminates TLS for you, wrong if this node is directly internet-facing with real value at stake. |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `SMTP_FROM` | no | If `SMTP_HOST`/`SMTP_USERNAME`/`SMTP_PASSWORD`/`SMTP_FROM` are all set (`SMTP_PORT` defaults to `587`), this node sends real outbound email (wallet-recovery codes, Trading Hub trade-attention alerts) via SMTP with STARTTLS. If unset, those flows fall back to their existing testnet-placeholder behavior (recovery codes shown directly in the API response instead of emailed; hub alerts stay in-app only). |
| `ANTHROPIC_API_KEY` | no | If set, enables the site-wide AI assistant (`POST /ai/chat`) - a Claude-powered, read-only chat widget that can look up wallet/reputation/loan-offer/hub-rules data and calculate loan repayment, but never signs or submits anything itself. If unset, the assistant returns a clear "not configured" error and the frontend widget shows that message instead of a reply. |
| `FORK_ALERT_EMAIL` | no | If set (and SMTP is also configured, see above), this node emails that address the instant it detects a chain fork - either via the periodic fork monitor (`api/fork_monitor.go`, every 60s) or immediately when `syncChainFromPeer` refuses a diverging chain mid-sync (`api/peer.go`). Every detected fork is always logged to `fork_events.log` in this node's `DATA_DIR` and reflected on `GET /health/sync` regardless of whether this is set - the email is a best-effort convenience on top of that, not the only signal. This does not attempt automated recovery - a detected fork always needs manual intervention (see `audit/HARDENING_HISTORY.md`). |

## Option A: Docker (recommended)

```
git clone https://github.com/Danielcruse/creditchaind.git
cd creditchaind
# edit docker-compose.yml: fill in SELF_URL, PEERS, NETWORK_SECRET
docker compose up -d
docker compose logs -f
```

Chain data persists in the `creditchaind-data` Docker volume across
restarts/upgrades.

> Note: this Dockerfile/compose setup was written but not yet
> build-tested in this environment (no local Docker available) -
> please confirm `docker compose up` actually works cleanly on your
> machine and report back if it doesn't.

## Option B: Build and run directly

```
git clone https://github.com/Danielcruse/creditchaind.git
cd creditchaind
go build -o creditchaind .

DATA_DIR=./data \
SELF_URL="http://your-public-host:8080" \
PEERS="http://existing-node-1:8080,http://existing-node-2:8080" \
NETWORK_SECRET="the-shared-secret" \
PORT=8080 \
./creditchaind
```

## Verifying you've actually joined

Once running, check that your node sees the same chain height and
latest block hash as an existing node:

```
curl http://localhost:8080/explorer?blocksLimit=1
curl http://an-existing-node:8080/explorer?blocksLimit=1
```

`blockHeight` and `latestBlockHash` should converge (may take a few
blocks to catch up on first sync).

## Known limitations (disclosed, not hidden)

- **No self-serve join flow.** Getting `NETWORK_SECRET` and being
  added to existing nodes' `PEERS` is currently a manual, out-of-band
  step with the network operator - there's no in-app request-to-join
  process yet.
- **No per-peer reputation or Sybil resistance.** Every node that has
  the shared secret is trusted equally. A fully open/permissionless
  P2P layer is a materially larger project than this doc covers, and
  is appropriate only after the security audit already planned for
  this project.
