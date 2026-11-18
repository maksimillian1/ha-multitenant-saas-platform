-- HAMSP-25 AC-12: the usage tiers under data, in a scratch database with the
-- usage migrations applied (usage-tiers.sh). Prints one line per result.
-- psql variables: events, tenants, days.
\set ON_ERROR_STOP on
\pset footer off
SELECT set_config('bench.events', :'events', false),
       set_config('bench.tenants', :'tenants', false),
       set_config('bench.days', :'days', false) \gset

-- The benchmark runs the policies itself, so their schedules do not interfere.
SELECT count(*) AS jobs_paused
FROM timescaledb_information.jobs j, LATERAL alter_job(j.job_id, scheduled => false)
WHERE j.proc_name LIKE 'policy_%';

CREATE TABLE bench_result (step int GENERATED ALWAYS AS IDENTITY, name text, value text);

-- Load: event times spread evenly over the last `days` days, in arrival order;
-- tenants interleaved; 80% api_calls with request attributes like the API
-- Gateway sends, 10% agent tokens, 10% egress bytes. One commit per day.
DO $$
DECLARE
    n       bigint := current_setting('bench.events')::bigint;
    tenants int    := current_setting('bench.tenants')::int;
    days    int    := current_setting('bench.days')::int;
    per_day bigint := n / days;
    span    float8 := days * 86400.0;
    t0      timestamptz := clock_timestamp();
    d       int;
BEGIN
    FOR d IN 0 .. days - 1 LOOP
        INSERT INTO usage_events (event_id, event_time, tenant_id, metric, quantity, attributes)
        SELECT uuidv7(),
               now() - make_interval(secs => span * (1 - g::float8 / n)),
               'tenant-' || lpad((abs(hashint8(g)) % tenants)::text, 3, '0'),
               CASE WHEN g % 10 < 8 THEN 'api_calls' WHEN g % 10 = 8 THEN 'agent_tokens' ELSE 'egress_bytes' END,
               CASE WHEN g % 10 < 8 THEN 1 WHEN g % 10 = 8 THEN 1 + abs(hashint8(g)) % 4000 ELSE 1 + abs(hashint8(g)) % 1000000 END,
               CASE WHEN g % 10 < 8 THEN jsonb_build_object(
                        'method', (ARRAY['GET', 'GET', 'GET', 'POST', 'PUT', 'DELETE'])[1 + g % 6],
                        'path', '/items/' || gen_random_uuid(),
                        'status', (ARRAY[200, 200, 200, 201, 404, 401, 500])[1 + g % 7],
                        'duration_ms', abs(hashint8(g)) % 250,
                        'request_bytes', abs(hashint8(g)) % 2048,
                        'response_bytes', abs(hashint8(g)) % 8192)
                    ELSE '{}'::jsonb END
        FROM generate_series(d * per_day + 1, (d + 1) * per_day) AS g;
        COMMIT;
    END LOOP;
    INSERT INTO bench_result (name, value) VALUES
        ('load: events', n::text),
        ('load: rate (events/s, with synchronous replication)', round(n / extract(epoch FROM clock_timestamp() - t0))::text);
    COMMIT;
END $$;

INSERT INTO bench_result (name, value)
SELECT 'size before compression, all chunks', pg_size_pretty(hypertable_size('usage_events'))
UNION ALL
SELECT 'bytes per event before compression (heap + indexes)', (hypertable_size('usage_events') / count(*))::text FROM usage_events;

-- Warm tier: the columnstore policy (chunks older than 8 days).
DO $$
DECLARE
    t0  timestamptz := clock_timestamp();
    job int;
BEGIN
    SELECT job_id INTO job FROM timescaledb_information.jobs
    WHERE proc_name = 'policy_compression' AND hypertable_name = 'usage_events';
    CALL run_job(job);
    INSERT INTO bench_result (name, value)
    VALUES ('columnstore policy run (s)', round(extract(epoch FROM clock_timestamp() - t0), 1)::text);
END $$;

INSERT INTO bench_result (name, value)
SELECT 'compressed chunks', count(*)::text FROM timescaledb_information.chunks
WHERE hypertable_name = 'usage_events' AND is_compressed
UNION ALL
SELECT 'compressed chunks: before / after', pg_size_pretty(sum(before_compression_total_bytes)) || ' / ' || pg_size_pretty(sum(after_compression_total_bytes))
FROM chunk_columnstore_stats('usage_events') WHERE compression_status = 'Compressed'
UNION ALL
SELECT 'compression ratio', round(sum(before_compression_total_bytes)::numeric / sum(after_compression_total_bytes), 1)::text
FROM chunk_columnstore_stats('usage_events') WHERE compression_status = 'Compressed';

-- Late duplicates through write_usage_events, one event per call like a
-- redelivery: originals in compressed chunks against uncompressed ones.
CREATE FUNCTION pg_temp.time_writes(sample regclass, fresh bool) RETURNS TABLE (p50 numeric, p95 numeric, stored bigint)
LANGUAGE plpgsql AS $$
DECLARE
    e  record;
    t0 timestamptz;
    ms float8[] := '{}';
    n  bigint := 0;
    k  bigint;
BEGIN
    FOR e IN EXECUTE format('SELECT * FROM %s', sample) LOOP
        PERFORM set_config('app.current_tenant_id', e.tenant_id, true);
        t0 := clock_timestamp();
        SELECT count(*) INTO k FROM write_usage_events(
            ARRAY[CASE WHEN fresh THEN uuidv7() ELSE e.event_id END], ARRAY[e.event_time], ARRAY[e.tenant_id],
            ARRAY[e.metric], ARRAY[e.quantity], ARRAY['{}']);
        ms := ms || extract(epoch FROM clock_timestamp() - t0) * 1000;
        n := n + k;
    END LOOP;
    RETURN QUERY SELECT round(percentile_cont(0.5) WITHIN GROUP (ORDER BY x)::numeric, 2),
                        round(percentile_cont(0.95) WITHIN GROUP (ORDER BY x)::numeric, 2), n
                 FROM unnest(ms) AS x;
END $$;

CREATE TEMP TABLE sample_compressed AS
SELECT * FROM usage_events WHERE event_time < now() - interval '8 days' - interval '2 hours' ORDER BY random() LIMIT 500;
CREATE TEMP TABLE sample_uncompressed AS
SELECT * FROM usage_events WHERE event_time > now() - interval '7 days' ORDER BY random() LIMIT 500;

INSERT INTO bench_result (name, value)
SELECT 'late duplicate into a compressed chunk: p50 / p95 ms (stored)', p50 || ' / ' || p95 || ' (' || stored || ')'
FROM pg_temp.time_writes('sample_compressed', false)
UNION ALL
SELECT 'late duplicate into an uncompressed chunk: p50 / p95 ms (stored)', p50 || ' / ' || p95 || ' (' || stored || ')'
FROM pg_temp.time_writes('sample_uncompressed', false);
INSERT INTO bench_result (name, value)
SELECT 'new late event into a compressed chunk: p50 / p95 ms (stored)', p50 || ' / ' || p95 || ' (' || stored || ')'
FROM pg_temp.time_writes('sample_compressed', true);

-- The hourly aggregate: a first full refresh, then one after the late events.
SELECT extract(epoch FROM clock_timestamp()) AS t0 \gset
CALL refresh_continuous_aggregate('usage_hourly', NULL, now() - interval '1 hour');
INSERT INTO bench_result (name, value)
SELECT 'aggregate: first refresh over all data (s)', round(extract(epoch FROM clock_timestamp()) - :t0, 1)::text;
SELECT extract(epoch FROM clock_timestamp()) AS t0 \gset
DO $$
DECLARE job int;
BEGIN
    SELECT j.job_id INTO job FROM timescaledb_information.jobs j
    LEFT JOIN timescaledb_information.continuous_aggregates c ON c.materialization_hypertable_name = j.hypertable_name
    WHERE j.proc_name = 'policy_refresh_continuous_aggregate' AND coalesce(c.view_name, j.hypertable_name) = 'usage_hourly';
    CALL run_job(job);
END $$;
INSERT INTO bench_result (name, value)
SELECT 'aggregate: policy refresh after 500 late events (s)', round(extract(epoch FROM clock_timestamp()) - :t0, 1)::text
UNION ALL
SELECT 'aggregate rows', count(*)::text FROM usage_hourly
UNION ALL
SELECT 'aggregate size', pg_size_pretty(hypertable_size(format('%I.%I', materialization_hypertable_schema, materialization_hypertable_name)))
FROM timescaledb_information.continuous_aggregates WHERE view_name = 'usage_hourly';

-- A billing read: one tenant's api_calls over all materialized hours (the
-- policy leaves out the last hour), from raw events (mostly uncompressed
-- here) and from the aggregate.
SELECT time_bucket(interval '1 hour', now() - interval '1 hour') AS upto \gset
SELECT extract(epoch FROM clock_timestamp()) AS t0 \gset
SELECT sum(quantity) AS raw_sum FROM usage_events
WHERE tenant_id = 'tenant-007' AND metric = 'api_calls' AND event_time < :'upto' \gset
INSERT INTO bench_result (name, value)
SELECT 'one tenant''s sum over all hours from raw events (ms)', round((extract(epoch FROM clock_timestamp()) - :t0) * 1000)::text;
SELECT extract(epoch FROM clock_timestamp()) AS t0 \gset
SELECT sum(quantity) AS agg_sum FROM usage_hourly
WHERE tenant_id = 'tenant-007' AND metric = 'api_calls' AND hour < :'upto' \gset
INSERT INTO bench_result (name, value)
SELECT 'one tenant''s sum over all hours from the aggregate (ms)', round((extract(epoch FROM clock_timestamp()) - :t0) * 1000)::text
UNION ALL
SELECT 'raw sum = aggregate sum', (:'raw_sum' = :'agg_sum')::text;

SELECT name, value FROM bench_result ORDER BY step;
