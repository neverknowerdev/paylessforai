# Durable self-update implementation plan

Status: proposed

## Outcome

PayLessForAI should continue to ship as one user-facing Go binary, but that
binary must run in two process roles:

1. a small, stable supervisor which owns the child process, update journal,
   database snapshot, promotion, and rollback; and
2. the application child which owns SQLite, the HTTP servers, the update
   scheduler, and the management UI.

The supervisor must never replace the active executable in place. Every build
is downloaded to an immutable version directory, verified, started as a
candidate, and only made active after it proves ready. If candidate startup or
migration fails, the supervisor restores the pre-update SQLite snapshot and
starts the previous known-good version.

This process boundary is the central durability requirement. The current
single-process startup applies migrations before binding the HTTP listener and
then exits on error, so an in-process `download + exec` implementation cannot
guarantee recovery.

## Product decisions

- Auto-update is enabled by default for official signed builds.
- The default channel is `releases`. `main` is explicitly labelled as an
  unstable snapshot channel.
- The default check interval is one hour. The API accepts 15 minutes through
  seven days; the scheduler adds up to 10% jitter and exponential backoff after
  remote failures.
- Development builds made with `go run` or plain `go build` report version
  `dev` and do not auto-update. This keeps local tests and source builds from
  unexpectedly replacing themselves.
- A manual check is allowed while auto-update is disabled. Installation remains
  a separate user action: **Check for updates**, then **Update now**.
- Auto-update never downgrades. Changing between `main` and `releases` when the
  target cannot be ordered against the current build requires manual
  confirmation.
- A version which failed startup is quarantined and is not attempted again
  every hour. A newer artifact or an explicit manual retry clears the block.
- Retain the active binary, the latest known-good fallback, and ten earlier
  successful binaries. Retain failure metadata but delete failed candidate
  binaries after fallback has started successfully.

## Current seams to change

The plan fits these existing boundaries:

- `cmd/paylessforai-app/main.go` is the thin executable entry point and becomes
  the supervisor/child mode switch.
- `app/runtime/runtime.go` owns application startup and becomes the child
  lifecycle with an explicit readiness/promotion gate.
- `internal/db/store.go` already embeds and applies ordered per-file SQLite
  migrations. It needs snapshot, preflight, and schema-reporting helpers.
- `internal/db/repositories/settings_repository.go` can persist the three typed
  update settings under namespaced keys without adding an application table.
- `app/controlplane` is the natural home for update settings/status/actions API
  handlers, backed by an injected update service interface.
- `internal/web` is embedded in the binary. Add a Settings view and a global
  update/rollback warning banner.
- The three existing GitHub Actions workflows validate Go, SQLite, and browser
  behavior, but none publishes versioned binaries.

## Process topology

The same executable supports two private modes:

```text
paylessforai-app (supervisor; stable PID seen by systemd/launchd/user)
  └─ paylessforai-app --internal-serve (active application child)
```

Normal invocation always enters supervisor mode. It acquires a single-instance
lock, loads the durable journal, resolves the active executable, and starts it
with the original public CLI flags plus an inherited supervisor handshake
pipe. Signals received by the supervisor are forwarded to the child.

The child never replaces binaries or changes the active pointer. It may check,
download, and preflight a candidate while continuing to serve traffic. To
install, it writes an idempotent update request, asks its HTTP server to drain,
and exits with a private update-requested exit code. The supervisor then owns
all state-changing steps.

The supervisor protocol is intentionally small and versioned. Every manifest
includes `min_supervisor_protocol`; an incompatible candidate is reported but
not installed. This lets the distributed artifact remain one binary while the
old supervisor stays resident throughout a risky update.

## Files and durable state

All updater-owned files live below the existing `-data-dir`:

```text
<data-dir>/
  paylessforai.db
  master.key
  updater/
    lock
    state.json
    history.jsonl
    RECOVERY.txt
    downloads/<update-id>.partial
    releases/<artifact-id>/paylessforai-app[.exe]
    backups/<update-id>/paylessforai.db
    logs/<update-id>/candidate.log
```

`state.json` is an atomically replaced, fsynced journal. It contains the
operation ID, phase, current and previous artifact identities, candidate path,
database snapshot path, timestamps, failure details, retry eligibility, and
the last acknowledged warning. It is separate from the application database
so restoring the database cannot erase the reason for a rollback.

`history.jsonl` is append-only and drives the Previous versions UI. Paths are
always resolved beneath `updater/releases`; pruning must refuse symlinks and
must never delete a path not owned by the updater.

Update settings remain in the existing `settings` table as:

- `updates.enabled` (`true` by default for official builds)
- `updates.channel` (`releases` by default; enum `releases|main`)
- `updates.check_interval_seconds` (`3600` by default)

The child owns these settings and the scheduler. The supervisor does not need
them to complete or recover an already-journaled operation.

## Update state machine

Every transition is persisted before its side effect. Repeating a transition
must be safe after a process or machine crash.

| Phase | Owner | Meaning and recovery |
| --- | --- | --- |
| `idle` | child | No operation is active. |
| `checking` | child | Fetching channel metadata; failure leaves the running version untouched. |
| `available` | child | A newer eligible manifest was found. |
| `downloading` | child | Writing a bounded `.partial` file. Resume or discard safely. |
| `verified` | child | Size, SHA-256, manifest signature, platform, and build identity passed. |
| `preflighting` | child | Candidate migrates a disposable SQLite snapshot and starts on isolated resources. |
| `staged` | child | Candidate is ready for a switchover request. |
| `draining` | child | New inference requests are rejected; in-flight requests receive a bounded graceful drain. |
| `snapshotting` | supervisor | Child has exited cleanly; a final standalone SQLite backup is being created. |
| `migrating` | candidate/supervisor | Candidate is applying migrations to the real database while public traffic remains gated. |
| `starting` | candidate/supervisor | Listener bound and internal startup checks are running. |
| `stabilizing` | supervisor | Candidate stays alive and ready for a bounded probation window while public traffic is still gated. |
| `promoted` | supervisor | Active pointer and history are committed, then the child opens its public gate. |
| `rolling_back` | supervisor | Candidate is stopped and the final pre-update database snapshot is restored. |
| `rolled_back` | supervisor | Previous binary is running; a durable UI warning contains the failed phase and error. |
| `needs_manual_recovery` | supervisor | Database restore or fallback startup failed; no destructive retry is attempted. |

Use a unique update ID as the idempotency key for API calls and journal events.
Only one check/download/install may run at a time.

## Candidate qualification and promotion

1. Resolve the target from GitHub using conditional requests (`ETag`) and strict
   HTTP timeouts.
2. Download to a same-filesystem temporary path with a configured maximum size.
3. Verify the signed manifest first, then the archive size and SHA-256, unpack
   without accepting absolute paths or traversal, and verify the candidate's
   embedded build identity.
4. Create an online SQLite snapshot while the old app is serving. Run the
   candidate in `--internal-preflight` against a disposable copy, with provider
   network refresh disabled and a random loopback listener. This catches most
   migration, config, secret-store, and startup regressions without downtime.
5. Ask the old child to stop accepting new inference calls and drain active
   streams. If it cannot drain before the update timeout, abort and restart the
   old version; do not force an automatic update through active traffic.
6. After the old child closes SQLite, create and verify a final standalone
   backup, checkpointing WAL safely. Record its hash and schema migration list.
7. Start the candidate on the real database and real listen address in gated
   mode. It may expose only the supervisor readiness handshake and health
   response until promotion; management and inference routes return
   `503 update_in_progress`.
8. Require: migrations completed, expected version/commit reported, database
   ping passed, listener successfully bound, supervisor nonce matched, and the
   process stayed ready for a 30-second stabilization window.
9. Persist the active pointer and `promoted` history atomically, then signal the
   candidate to open its public gate.
10. Prune only after promotion. Never delete the fallback or the backup for the
    most recent promotion until the next known-good update.

The handshake pipe is authoritative; polling the public port alone could talk
to an unrelated process and must not promote a candidate.

## Migration and database recovery policy

The current migrator commits each migration file separately. If migration N+1
fails after N committed, restarting the old binary does not restore the old
schema. Therefore binary rollback and database rollback must be one operation.

For each update:

1. Preflight all candidate migrations against a disposable backup.
2. Stop writes and take a final SQLite backup immediately before the real
   migration.
3. Keep public traffic gated until promotion, so restoring the snapshot cannot
   discard post-update user requests.
4. If migration or startup fails, close the candidate, atomically restore the
   standalone backup, remove stale `-wal`/`-shm` files while no process holds
   the database, verify integrity and the recorded migration list, then launch
   the previous binary.
5. If restore verification or fallback startup fails, enter
   `needs_manual_recovery`, preserve the candidate, previous binary, database,
   snapshot, journal, and logs, and write exact recovery paths to
   `RECOVERY.txt`.

Do not add automatic down migrations for this updater. Snapshot restoration is
exact and is safer than trying to reverse arbitrary SQL after a partial
multi-file migration.

In addition, adopt an expand/contract migration rule:

- Auto-update-eligible migrations must be transactional and backward-readable
  by the immediately previous version.
- Drops, destructive rewrites, incompatible renames, or meaning changes require
  a staged release: expand first, migrate/backfill, and contract only after the
  supported rollback window has passed.
- CI runs both `old binary -> new database` and `new binary -> restored old
  database` fixtures for every release candidate.
- The manifest carries a schema compatibility range. An incompatible target is
  shown in the UI as requiring manual upgrade and is never auto-installed.

## Artifact and manifest contract

Add `internal/buildinfo` with values injected by `-ldflags`:

- semantic version or main snapshot identity
- full Git commit SHA
- channel (`releases`, `main`, or `dev`)
- build timestamp
- target OS and architecture
- supervisor protocol version
- official-build boolean

Publish one immutable archive per supported target, initially:

```text
paylessforai_<version>_linux_amd64.tar.gz
paylessforai_<version>_linux_arm64.tar.gz
paylessforai_<version>_darwin_amd64.tar.gz
paylessforai_<version>_darwin_arm64.tar.gz
paylessforai_<version>_windows_amd64.zip
```

The release also contains `update-manifest.json`,
`update-manifest.json.sig`, and `checksums.txt`. The manifest schema includes:

```json
{
  "schema": 1,
  "channel": "releases",
  "version": "v1.2.3",
  "commit": "full-sha",
  "published_at": "RFC3339 timestamp",
  "min_supervisor_protocol": 1,
  "schema_compatibility": {"min": 1, "max": 13},
  "artifacts": [
    {"os": "linux", "arch": "amd64", "url": "release asset URL", "size": 123, "sha256": "..."}
  ]
}
```

Sign the canonical manifest with a pinned Ed25519 public key embedded in
official clients. Protect the signing secret with the GitHub release
environment and restricted workflow permissions. Also publish GitHub artifact
attestations for auditability. Checksums without a trusted signature are not a
sufficient update trust boundary.

## Channel discovery

### Releases

Publish stable versions from annotated `v*` tags. The workflow runs the full
qualification suite, builds and signs assets, creates a draft release, uploads
the manifest last, and only then publishes the release. The updater uses the
latest non-draft, non-prerelease release whose signed manifest contains a
matching platform artifact.

### Main

After every successful push to `main`, publish an immutable prerelease tagged
`main-<full-sha>`. Do not use expiring GitHub Actions artifacts and do not
replace an asset under a mutable URL. The updater lists prereleases, selects the
newest signed `main-*` artifact, and compares commit identity/publish sequence.
Retain a bounded number of main snapshot releases on GitHub, but never let the
cleanup job touch stable releases.

The repository is public, so downloads do not require a user GitHub token.
Handle API rate limits and offline operation as ordinary check failures; neither
may stop the running app.

## HTTP API

Inject a narrow `updates.Service` into `controlplane.Server` and add:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/updates` | Settings, build info, phase, available target, last check/error, warning, and history summary. |
| `PUT` | `/api/updates/settings` | Validate and atomically update enabled/channel/interval; reset the scheduler timer. |
| `POST` | `/api/updates/check` | Start or join an idempotent manual check. |
| `POST` | `/api/updates/install` | Install the currently verified target; reject stale target IDs. |
| `POST` | `/api/updates/warning/acknowledge` | Hide the current banner without deleting history. |
| `GET` | `/api/updates/history` | Return bounded successful, failed, and rolled-back attempts. |

Long actions return `202 Accepted` with an operation ID. The UI polls the
resource; requests do not stay open through download or restart. API error
responses use stable codes such as `update_busy`, `update_not_available`,
`update_untrusted`, `update_incompatible`, and `update_requires_restart`.

Because management endpoints are currently unauthenticated on loopback, update
actions must continue to require loopback access. If remote UI access is added
later, update mutation endpoints require explicit authentication and CSRF
protection before exposing them.

## UI

Add a **Settings** navigation item and an **Updates** card with:

- current version, commit, channel, and build time;
- automatic updates toggle;
- channel selector with an unstable warning for `main`;
- check interval selector/input;
- last successful check and any check error;
- **Check for updates** button;
- target version and release notes link after a successful check;
- **Update now** button with download/preflight/restart progress;
- previous versions table with installed time, channel, outcome, and rollback
  reason.

After automatic rollback, show a persistent banner on every view. It includes
the attempted version, failed phase, safe error summary, fallback version, and
links to Settings/history and a copyable diagnostic block. Acknowledge hides
the banner but does not delete the record.

Never render raw candidate logs as HTML. Redact configured secrets and bound
log size before returning diagnostics.

## GitHub Actions design

Create reusable qualification and build jobs rather than publishing from the
existing independent workflows without a gate:

1. `.github/workflows/ci.yml` (or reusable `workflow_call` jobs) runs unit,
   race, vet, SQLite repository integration, browser E2E, generated-model diff,
   migration compatibility, updater state-machine, and rollback fault tests.
2. `.github/workflows/publish-main.yml`, triggered by pushes to `main`, depends
   on all qualification jobs, builds the target matrix with reproducible
   ldflags, signs the manifest, creates `main-<sha>` as a prerelease, and
   publishes the manifest last.
3. `.github/workflows/release.yml`, triggered by annotated `v*` tags, validates
   the version, repeats qualification, builds/signs, creates a draft GitHub
   Release, uploads all target assets plus attestations, and publishes only
   after every asset is present.
4. `.github/workflows/prune-main-releases.yml` deletes only old releases whose
   tag matches the exact `main-<40 hex chars>` pattern and keeps a documented
   retention window.

Workflow permissions default to `contents: read`. Only publication jobs receive
`contents: write`; attestation jobs receive `id-token: write` and
`attestations: write`. Pin third-party actions to reviewed commit SHAs before
production release signing.

## Implementation phases

### Phase 1: Build identity and artifact contract

- Add `internal/buildinfo`, `--version`, and JSON build identity output.
- Define and test canonical manifest parsing, platform selection, version/channel
  eligibility, signatures, size limits, and download path safety.
- Add release/main build matrices and dry-run artifact validation without
  enabling client installation.

Exit gate: a CI-built archive reports the expected version and commit, its
manifest/signature verifies, and tampered/wrong-platform assets are rejected.

### Phase 2: Supervisor and crash-safe journal

- Split normal supervisor mode from `--internal-serve` child mode.
- Add signal forwarding, single-instance locking, active/fallback resolution,
  atomic journal transitions, log capture, and restart policy.
- Preserve all existing CLI arguments when starting children.
- Add recovery for a crash at every journal boundary.

Exit gate: fault-injection tests kill the supervisor or child after each phase;
the next invocation deterministically starts the committed active version or
the previous fallback and never loops on a failed candidate.

### Phase 3: SQLite snapshot and candidate gate

- Add WAL-safe online backup, integrity verification, atomic restore, and
  migration-list capture.
- Refactor runtime startup so external provider refresh is bounded and occurs
  after core readiness; listener bind is explicit rather than inferred from a
  goroutine.
- Add preflight mode, real-database gated startup, handshake nonce, drain, and
  stabilization window.
- Enforce expand/contract compatibility metadata in migration tests.

Exit gate: injected migration failure, bind failure, immediate process exit,
health timeout, corrupt download, corrupt snapshot, and power loss all produce
the specified fallback or `needs_manual_recovery` result with durable details.

### Phase 4: Scheduler, API, and UI

- Add typed settings service using the existing settings repository.
- Add ETag-aware checker, jitter/backoff, singleflight operation control,
  download/preflight orchestration, failed-version quarantine, and manual
  two-step actions.
- Add control-plane handlers, Settings UI, progress polling, version history,
  and persistent rollback banner.

Exit gate: API integration and Playwright tests cover defaults, validation,
disabled auto-update/manual check, both channels, check-to-install flow,
restart progress, rollback warning, acknowledgement, and history.

### Phase 5: End-to-end publication and staged rollout

- Publish a bootstrap release containing the supervisor architecture.
- Install that release manually on test machines; the pre-existing binary
  cannot retroactively supervise its own replacement safely.
- Publish a deliberately healthy candidate and verify update/promotion on every
  supported OS/architecture.
- Publish fault candidates in a private test repository: bad checksum,
  migration error, bind error, startup crash, and readiness timeout. Verify
  automatic rollback and UI diagnostics.
- Enable main-channel auto-update for canaries first, then stable releases.

Exit gate: repeated real process updates survive injected failure and machine
restart without data loss, and GitHub exposes immutable signed assets only
after all qualification checks pass.

## Required tests

### Unit and property tests

- state transition legality and idempotency;
- journal atomicity and truncated/corrupt journal recovery;
- manifest signature, hash, size, archive traversal, OS/architecture, channel,
  and version eligibility;
- scheduler reset, jitter bounds, backoff, ETag/304, and failed-version
  quarantine;
- retention path containment and protected-file rules;
- secret redaction and bounded diagnostics.

### Process integration tests

- healthy candidate promotion;
- candidate exits before handshake;
- candidate reports wrong version/nonce;
- production listener bind failure;
- readiness/stabilization timeout;
- old child drain timeout;
- supervisor/child termination at every persisted phase;
- fallback launch failure -> `needs_manual_recovery`;
- concurrent manual/automatic update requests remain single-operation.

Use small purpose-built fixture binaries rather than mocks for process ownership
and exit behavior.

### Migration integration tests

- forward migration succeeds on a realistic existing database;
- migration N commits and N+1 fails, then snapshot restoration exactly matches
  the original schema and data;
- WAL contains uncheckpointed writes when snapshot starts;
- old binary starts against every auto-update-eligible new schema;
- corrupt backup is detected before replacing the live database;
- no candidate traffic is accepted before promotion, so rollback loses no
  post-snapshot requests.

### Browser E2E

- official fixture build defaults to enabled/release/one hour;
- dev build explains why update is unavailable;
- manual check shows an available version and requires a second click;
- progress survives page refresh and process restart;
- rollback banner shows the failed version/phase/error and fallback version;
- previous versions and acknowledgement behavior remain durable.

## Acceptance criteria

The feature is complete only when all of the following are demonstrated with
persisted evidence:

- An eligible main or release artifact is found within the configured interval
  and manual check works while automatic updates are disabled.
- Only signed, matching, immutable artifacts can become candidates.
- The old executable and a verified database snapshot remain available until
  the candidate is promoted.
- A candidate is never promoted before migration, listener bind, readiness,
  identity handshake, and stabilization all pass.
- Any migration/startup failure before promotion restores the exact database
  snapshot, restarts the previous binary, suppresses the failed candidate from
  automatic retry, and displays a durable warning with useful diagnostics.
- A crash or power loss at every journaled phase recovers deterministically on
  the next invocation.
- If automatic recovery itself fails, the system preserves all recovery assets
  and enters a visible `needs_manual_recovery` state instead of deleting data or
  retrying destructively.
- GitHub does not publish update manifests until all tests and all platform
  assets have succeeded.

## Explicit non-goals for v1

- Silent downgrade between incomparable channels.
- Automatic rollback after the promoted version has accepted user writes. That
  is a runtime crash-restart policy and cannot safely restore an older database
  snapshot without a separate data-reconciliation design.
- Destructive schema contraction inside the automatic rollback window.
- Updating an already-running legacy binary that does not contain the
  supervisor; one manual bootstrap replacement is required.
- Remote management of update settings before management API authentication is
  implemented.
