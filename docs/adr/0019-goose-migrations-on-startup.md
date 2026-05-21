# ADR-019: Goose for schema migrations, embedded and run on startup

**Status:** Accepted (2026-05-21)

**Context.** Phase 1 introduces ≥7 Postgres tables (devices, operators, refresh_tokens, clients, sites, operator_sites, audit_log, enrollment_idempotency) and the rest of Phase 1 plus later phases will add more. We need a migration story that satisfies:

- Idempotent up/down semantics.
- Embeddable in the deployed binary so deploy = migrate (no separate ops surface).
- Testable against the testcontainers Postgres used by the integration tests.
- AFK-agent-friendly: "add a SQL file in this directory" should be the entire mental model an agent needs.

Candidates considered:

1. **goose** — Go-native library, SQL-file or Go-function migrations, mature ecosystem.
2. **golang-migrate/migrate** — similar feature set, similar ergonomics; goose slightly cleaner for Go-function migrations.
3. **Atlas (declarative)** — schema-as-config tool that diffs declared state against the live DB to generate migrations.
4. **sqlc + separate migration tool** — sqlc handles query-time type safety; orthogonal to migrations.
5. **Hand-rolled pgx** — `os.ReadDir`, `pgx.Exec`, done.

Why not hand-rolled: at ≥7 tables in Phase 1 and growing per phase, every test setup and CI fixture reinvents what a library gives you. Boilerplate cost exceeds library cost.

Why not Atlas-declarative: schema-as-config is appealing, but diff-generated migrations are harder to *review* than imperative SQL. For an AFK-agent codebase where the architectural reviewer reads every change, "agent writes a SQL file, human reviews the SQL" is more legible than "agent edits a schema spec, tool generates a migration, human reviews the generated SQL." Review legibility matters more here than DSL elegance.

Why goose over golang-migrate: equivalent for our use case. goose's Go-function migration mode (any migration needing data-massaging logic, not just DDL) reads cleaner. Either would be defensible; tie broken on familiarity.

A second axis: **when do migrations run?**

- **On startup** of each binary, gated by a Postgres advisory lock so only one of N service instances actually runs them.
- **Separate CI step** before the new container is deployed.

On-startup is simpler ops and aligns with "deploy = migrate." Separate-step is safer for high-risk migrations but adds CI complexity and a "migration succeeded but deploy failed" reconcile question. At Phase 1's scale (multi-AZ Postgres, modest table count, low traffic), on-startup's risk profile is acceptable. The split can be revisited in Phase 3 or 4 if a specific migration is genuinely scary.

**Decision.** Schema migrations use **goose**, with SQL-file migrations embedded into the deployed binaries via `embed.FS`, run on startup, serialized across instances via a Postgres advisory lock.

Concrete shape:

- Migrations live in `internal/cp/storage/migrations/<NN>_<slug>.sql`, numbered sequentially from `001_initial.sql`.
- Each migration uses `-- +goose Up` / `-- +goose Down` markers.
- The `cp-api` and `cp-ingest` binaries each `embed.FS` the migrations directory.
- On startup, each binary acquires a fixed-key Postgres advisory lock (`pg_advisory_lock(<constant>)`), runs `goose.UpContext`, releases the lock, then proceeds to normal startup. Only one binary instance runs migrations per deploy; the others wait for the lock and find nothing to apply.
- Integration tests apply migrations against a testcontainers Postgres before each test package's setup.
- A `make migrate-status` target (or `cp-api migrate status` subcommand) prints applied/pending for ops debugging.

**Consequences.**

- (+) Single tool, single mental model: agents add a new `.sql` file, goose picks it up, everything in CI/dev/prod follows the same path.
- (+) Deploy = migrate. No separate ops surface for the team to remember.
- (+) Advisory-lock serialization handles the multi-instance Fargate case without fancy coordination.
- (+) Embedded migrations mean the deployed binary's schema is always in sync with its code — no "wait, did the migration run yet?" failure mode.
- (+) Imperative SQL is review-legible; an agent's migration PR is read as-is, not as a delta against a declarative spec.
- (-) On-startup migrations couple deploy success to migration safety. A failed migration takes down the service. Phase 1's migrations are small and low-risk; this is acceptable. The decision is revisitable for genuinely high-risk migrations.
- (-) Locked into goose's `+goose` comment syntax. Switching tools later requires rewriting every existing migration file's headers (mechanical but real).
- (-) Down migrations exist but should be treated as last-resort recovery, not as a routine rollback path. Forward-only is the default operational posture.

**Verification.** TBD — added at implementation. Integration tests cover:

- `tests/integration/storage_test.go::TestMigrationsApplyClean` — fresh DB, goose runs all migrations to the latest version, schema matches expectations.
- `tests/integration/storage_test.go::TestMigrationsAreIdempotentOnRestart` — second startup against an already-migrated DB is a no-op.
- `tests/integration/storage_test.go::TestAdvisoryLockSerializes` — two simultaneous startup attempts against a clean DB result in exactly one running migrations (and the other waiting + finding nothing to apply).
