-- A tenant table without RLS: the guardrail must still report it, even though
-- the tenants table is exempt as a platform table.
CREATE TABLE no_rls_notes (id int PRIMARY KEY, tenant_id text NOT NULL);
