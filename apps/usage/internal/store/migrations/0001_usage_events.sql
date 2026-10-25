-- Usage events (ADR-0006): one table partitioned by day on event_time. The
-- primary key is the dedup key of ADR-0007; it must include the partition column.
CREATE TABLE usage_events (
    event_id   uuid        NOT NULL,
    event_time timestamptz NOT NULL,
    tenant_id  text        NOT NULL CHECK (tenant_id <> ''),
    metric     text        NOT NULL,
    quantity   bigint      NOT NULL CHECK (quantity > 0),
    attributes jsonb       NOT NULL DEFAULT '{}',
    stored_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, event_id, event_time)
) PARTITION BY RANGE (event_time);

-- FORCE makes the table owner subject to RLS too (ADR-0002).
ALTER TABLE usage_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_events FORCE ROW LEVEL SECURITY;

-- current_setting(..., true) returns NULL or '' when no tenant is set, so a
-- session without tenant context sees no rows and cannot insert (fail closed).
CREATE POLICY tenant_isolation ON usage_events
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));

-- RETURNING needs SELECT. Usage Writer gets no rights on the partitions, so it
-- can reach rows only through this table and its policy.
GRANT SELECT, INSERT ON usage_events TO usage_writer;

-- Creates the UTC day partitions from_day..to_day that do not exist yet. Runs
-- as the owner, so Usage Writer can call it without owning the table. Every
-- partition gets forced RLS without a policy: direct access to a partition
-- returns nothing to anyone but a superuser.
CREATE FUNCTION ensure_usage_partitions(from_day date, to_day date) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    d    date;
    part text;
BEGIN
    IF from_day IS NULL OR to_day IS NULL OR to_day < from_day OR to_day - from_day > 31 THEN
        RAISE EXCEPTION 'invalid partition range % .. %', from_day, to_day;
    END IF;
    -- Serialises concurrent writers creating the same day.
    PERFORM pg_advisory_xact_lock(5000002);
    FOR d IN SELECT generate_series(from_day, to_day, interval '1 day')::date LOOP
        part := 'usage_events_' || to_char(d, 'YYYYMMDD');
        CONTINUE WHEN to_regclass(part) IS NOT NULL;
        EXECUTE format('CREATE TABLE %I PARTITION OF usage_events FOR VALUES FROM (%L) TO (%L)',
                       part, d::text || ' 00:00:00+00', (d + 1)::text || ' 00:00:00+00');
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', part);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', part);
    END LOOP;
END $$;

REVOKE ALL ON FUNCTION ensure_usage_partitions(date, date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION ensure_usage_partitions(date, date) TO usage_writer;
