-- Negative fixture for UnprotectedTables: both tables must be reported.
CREATE TABLE no_rls_notes (id int PRIMARY KEY, tenant_id text NOT NULL);

CREATE TABLE not_forced_notes (id int PRIMARY KEY, tenant_id text NOT NULL);
ALTER TABLE not_forced_notes ENABLE ROW LEVEL SECURITY;

-- Not multi-tenant: no tenant_id column, so it is not reported.
CREATE TABLE settings (key text PRIMARY KEY, value text);

-- The parent is protected, its partition is not: the partition is reported.
CREATE TABLE parted_notes (id int, tenant_id text NOT NULL, at date NOT NULL) PARTITION BY RANGE (at);
ALTER TABLE parted_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE parted_notes FORCE ROW LEVEL SECURITY;
CREATE TABLE parted_notes_2026 PARTITION OF parted_notes FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
