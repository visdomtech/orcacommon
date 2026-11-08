-- Test migration for rebaseline tests. Creates a sentinel table
-- to verify whether migration SQL was executed.
CREATE TABLE migration_sentinel (id INT NOT NULL DEFAULT 1);
