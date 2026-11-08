# ADR: PostgreSQL Rebaseline Operation

## Status
Accepted

## Context
The `postgres` package manages Atlas-based database migrations using the `atlas_schema_revisions` history table. After migration files are consolidated (e.g., squashing multiple migrations into one), the history table contains stale entries that no longer match the migration directory. Without a rebaseline mechanism, operators must manually manipulate the history table — a fragile and error-prone process.

The original `Migrator.IsBaseline` mechanism (now removed) only handled the initial deployment baseline (first migration marked as already applied). It did not support replacing an existing history after consolidation.

## Decision
Add `DBConfig.RebaselineLastMigration` (env: `DB_REBASELINE_LAST_MIGRATION`) to enable an atomic rebaseline operation. When set:

1. **Validate all preconditions** before any persistent change:
   - Migration directory checksum integrity (atlas.sum matches files)
   - Exactly one migration file in the directory
   - File version matches configured version
   - History table exists and is non-empty
   - Latest revision version matches configured version
   - Latest revision fully applied with no error

2. **Atomically** (single PostgreSQL transaction):
   - Rename `atlas_schema_revisions` to `atlas_schema_revisions_backup_<UTC nanosecond timestamp>`
   - Rename the primary-key index to avoid name collision
   - Create a fresh `atlas_schema_revisions` table
   - Write one baseline revision directly (bypassing Atlas executor)

3. **Fail closed**: invalid preconditions return errors; no fallback to normal migration execution.

### Rejected Alternatives

1. **Copy rows into a backup with `CREATE TABLE AS`**: Adds data-copy I/O and does not preserve the complete table definition (indexes, constraints). `ALTER TABLE RENAME` preserves the old table directly with zero copy.

2. **Delete old revisions and insert the baseline in the same table**: Does not produce a separate backup table. Higher recovery risk.

3. **Use `Executor.Pending()` with `WithBaselineVersion` inside the transaction**: Atlas supports this, but it adds executor and dirty-check behavior to a metadata-only operation. Direct revision insertion is smaller and guarantees no migration SQL execution.

4. **Add rebaseline options to `Migrator` or `NewMigrator`**: The setting belongs to database configuration, not migration behavior. This would also change the public constructor contract.

5. **Ignore mismatches and continue normal migrations**: Hides unsafe rebaseline configurations. Enabled runs must fail closed.

6. **Treat a repeated valid run as a no-op**: Changes the required matching behavior. Each valid run creates a distinct backup as designed.

### Connection Pinning
The advisory lock and rebaseline transaction use a dedicated `*sql.Conn` pinned from the pool. This prevents the session-level advisory lock from drifting between pooled connections. The `pgRevisions` struct was generalized to an `execQuerier` interface (satisfied by `*sql.DB`, `*sql.Conn`, and `*sql.Tx`) to support transaction-bound revision writes.

## Consequences

### Positive
- Operators can safely consolidate migration histories in production
- Atomic operation with PostgreSQL transactional DDL guarantees all-or-nothing
- Old history preserved as a backup table for manual recovery
- Migration file SQL never executes during rebaseline
- All preconditions validated before any persistent change

### Negative
- Each valid run creates another backup table (operators must remove the setting)
- Recovery from backup is manual (no library-provided restore)
- Requires exactly one migration file in the directory

### Risks
- Backup table accumulation if the setting is not removed after rollout
- Timestamp collision is theoretically possible but astronomically unlikely (nanosecond precision)
- Schema drift detection tools (e.g., Atlas CLI) will report drift after rebaseline — this is expected

## Configuration

| Field | Env Tag | Default | Description |
|-------|---------|---------|-------------|
| `RebaselineLastMigration` | `REBASELINE_LAST_MIGRATION` | empty | Set to the last applied migration version to enable rebaseline. Remove after successful rollout. |

With the `DB_` prefix: `DB_REBASELINE_LAST_MIGRATION=20260101000000`.

## Operational Notes
- Remove `DB_REBASELINE_LAST_MIGRATION` after a successful rollout
- Backup tables follow the pattern `atlas_schema_revisions_backup_YYYYMMDDTHHMMSSnnnnnnnnnZ`
- The library does not restore, retain, or delete backups
- Schema drift checks and backup retention remain outside the library
