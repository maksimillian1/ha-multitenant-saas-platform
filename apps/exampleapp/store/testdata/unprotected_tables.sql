-- Negative fixture for UnprotectedTables: both tables must be reported.
CREATE TABLE no_rls_notes (id int PRIMARY KEY, tenant_id text NOT NULL);

CREATE TABLE not_forced_notes (id int PRIMARY KEY, tenant_id text NOT NULL);
ALTER TABLE not_forced_notes ENABLE ROW LEVEL SECURITY;

-- Not multi-tenant: no tenant_id column, so it is not reported.
CREATE TABLE settings (key text PRIMARY KEY, value text);
