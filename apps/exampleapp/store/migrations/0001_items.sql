-- Items of ExampleApp. Every row belongs to one tenant (Zitadel Organization ID).
CREATE TABLE items (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  text        NOT NULL CHECK (tenant_id <> ''),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX items_tenant_id_created_at_idx ON items (tenant_id, created_at);

-- FORCE makes the table owner subject to RLS too (ADR-0002).
ALTER TABLE items ENABLE ROW LEVEL SECURITY;
ALTER TABLE items FORCE ROW LEVEL SECURITY;

-- current_setting(..., true) returns NULL or '' when no tenant is set, so a
-- session without tenant context sees no rows and cannot insert (fail closed).
CREATE POLICY tenant_isolation ON items
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON items TO exampleapp;
