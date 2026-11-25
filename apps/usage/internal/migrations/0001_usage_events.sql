-- Usage events (ADR-0008): a TimescaleDB hypertable with 1-day chunks on
-- event_time. The primary key is the dedup key of ADR-0007; it must include
-- the time column. CNPG's Database resource creates the extension.
CREATE TABLE usage_events (
    event_id   uuid        NOT NULL,
    event_time timestamptz NOT NULL,
    tenant_id  text        NOT NULL CHECK (tenant_id <> ''),
    metric     text        NOT NULL,
    quantity   bigint      NOT NULL CHECK (quantity > 0),
    attributes jsonb       NOT NULL DEFAULT '{}',
    stored_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, event_id, event_time)
);
SELECT create_hypertable('usage_events', by_range('event_time', interval '1 day'));

-- TimescaleDB refuses columnstore and continuous aggregates on a table with
-- row-level security, so this database isolates tenants without RLS
-- (ADR-0008): no application role has privileges on any table. Usage Writer
-- writes through write_usage_events, readers use the *_tenant views. Both
-- enforce app.current_tenant_id the way the RLS policies of ADR-0002 do.
REVOKE ALL ON usage_events FROM PUBLIC;

-- Stores a batch of one tenant's events and returns the ones that were new.
-- It rejects the whole batch when no tenant is set or any event belongs to
-- another tenant (RLS WITH CHECK). Runs as the owner; search_path is pinned.
CREATE FUNCTION write_usage_events(
    ids uuid[], times timestamptz[], tenants text[], metrics text[], quantities bigint[], attrs text[]
) RETURNS TABLE (event_id uuid, event_time timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    tenant text := current_setting('app.current_tenant_id', true);
    n      int  := cardinality(ids);
BEGIN
    IF tenant IS NULL OR tenant = '' THEN
        RAISE EXCEPTION 'no tenant in app.current_tenant_id' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF cardinality(times) <> n OR cardinality(tenants) <> n OR cardinality(metrics) <> n
       OR cardinality(quantities) <> n OR cardinality(attrs) <> n THEN
        RAISE EXCEPTION 'arrays of different length';
    END IF;
    IF EXISTS (SELECT 1 FROM unnest(tenants) AS t(id) WHERE t.id IS DISTINCT FROM tenant) THEN
        RAISE EXCEPTION 'batch has events of another tenant than %', tenant USING ERRCODE = 'insufficient_privilege';
    END IF;
    -- ON CONSTRAINT: the output columns event_id and event_time would make a
    -- column list ambiguous inside PL/pgSQL.
    RETURN QUERY
    INSERT INTO usage_events AS e (event_id, event_time, tenant_id, metric, quantity, attributes)
    SELECT u.id, u.t, u.tenant, u.metric, u.quantity, u.attrs::jsonb
    FROM unnest(ids, times, tenants, metrics, quantities, attrs) AS u(id, t, tenant, metric, quantity, attrs)
    ON CONFLICT ON CONSTRAINT usage_events_pkey DO NOTHING
    RETURNING e.event_id, e.event_time;
END $$;

REVOKE ALL ON FUNCTION write_usage_events(uuid[], timestamptz[], text[], text[], bigint[], text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION write_usage_events(uuid[], timestamptz[], text[], text[], bigint[], text[]) TO usage_writer;

-- The raw events of the session's tenant (RLS USING). security_barrier keeps
-- the caller's own conditions from running before the tenant filter. Without
-- a tenant the view is empty.
CREATE VIEW usage_events_tenant WITH (security_barrier) AS
SELECT event_id, event_time, tenant_id, metric, quantity, attributes, stored_at
FROM usage_events
WHERE tenant_id = current_setting('app.current_tenant_id', true);

REVOKE ALL ON usage_events_tenant FROM PUBLIC;
GRANT SELECT ON usage_events_tenant TO usage_reader;
