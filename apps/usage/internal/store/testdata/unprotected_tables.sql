-- Negative fixture for UnprotectedTables. A relation is reported when an
-- application role can reach it without tenant isolation.

-- Granted, no RLS: reported.
CREATE TABLE no_rls_notes (id int PRIMARY KEY, tenant_id text NOT NULL);
GRANT SELECT ON no_rls_notes TO usage_reader;

-- Granted, RLS not forced: reported.
CREATE TABLE not_forced_notes (id int PRIMARY KEY, tenant_id text NOT NULL);
ALTER TABLE not_forced_notes ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON not_forced_notes TO PUBLIC;

-- No RLS, but nobody but the owner has a privilege: not reported (ADR-0008).
CREATE TABLE closed_notes (id int PRIMARY KEY, tenant_id text NOT NULL);

-- Forced RLS and granted: not reported.
CREATE TABLE rls_notes (id int PRIMARY KEY, tenant_id text NOT NULL);
ALTER TABLE rls_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE rls_notes FORCE ROW LEVEL SECURITY;
GRANT SELECT ON rls_notes TO usage_reader;

-- Not multi-tenant: no tenant_id column, so it is not reported.
CREATE TABLE settings (key text PRIMARY KEY, value text);
GRANT SELECT ON settings TO usage_reader;

-- A granted view without the tenant filter: reported.
CREATE VIEW all_notes AS SELECT id, tenant_id FROM closed_notes;
GRANT SELECT ON all_notes TO usage_reader;

-- A granted view with the filter but without security_barrier: reported.
CREATE VIEW leaky_notes AS
SELECT id, tenant_id FROM closed_notes WHERE tenant_id = current_setting('app.current_tenant_id', true);
GRANT SELECT ON leaky_notes TO usage_reader;

-- A granted security_barrier view with the filter: not reported.
CREATE VIEW tenant_notes WITH (security_barrier) AS
SELECT id, tenant_id FROM closed_notes WHERE tenant_id = current_setting('app.current_tenant_id', true);
GRANT SELECT ON tenant_notes TO usage_reader;
