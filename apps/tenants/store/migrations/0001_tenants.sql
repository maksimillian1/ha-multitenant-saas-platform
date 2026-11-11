-- The tenant registry of Tenant Management (ADR-0012). A platform table, not
-- tenant data: one row per tenant, read and written only by Tenant
-- Management's role. It has a tenant_id-like key but no RLS on purpose; the
-- RLS guardrail lists it in PlatformTables (store/migrate.go).
CREATE TABLE tenants (
    tenant_id   text        PRIMARY KEY CHECK (tenant_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    -- name and admin_email are cleared when the tenant is deleted (tombstone).
    name        text        CHECK (name <> ''),
    admin_email text,
    state       text        NOT NULL CHECK (state IN ('provisioning', 'active', 'suspended', 'deleting', 'deleted')),
    -- The parts of the tenant created so far outside this table (core.Parts*).
    parts       text[]      NOT NULL DEFAULT '{}',
    last_error  text,
    created_by  text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_by  text,
    deleted_at  timestamptz,
    CHECK ((state = 'deleted') = (deleted_at IS NOT NULL)),
    CHECK (state = 'deleted' OR (name IS NOT NULL AND admin_email IS NOT NULL))
);

-- A name is unique among tenants that are not deleted, ignoring case.
CREATE UNIQUE INDEX tenants_live_name_idx ON tenants (lower(name)) WHERE state <> 'deleted';

REVOKE ALL ON tenants FROM PUBLIC;
-- No DELETE: a deleted tenant stays as a tombstone.
GRANT SELECT, INSERT, UPDATE ON tenants TO tenants;
