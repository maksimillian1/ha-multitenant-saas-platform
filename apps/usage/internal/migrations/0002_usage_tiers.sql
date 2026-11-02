-- Hot and warm tiers in one hypertable (ADR-0008). Chunks stay uncompressed
-- for 8 days, the Kafka topic retention (7 days, ADR-0005) + 1 day, so every
-- redelivery lands in an uncompressed chunk. Older chunks go to the
-- columnstore, segmented by tenant; the dedup key stays enforced there too.
ALTER TABLE usage_events SET (
    timescaledb.enable_columnstore,
    timescaledb.segmentby = 'tenant_id',
    timescaledb.orderby = 'event_time, event_id'
);
CALL add_columnstore_policy('usage_events', after => interval '8 days');

-- Raw events are kept 13 months (Sizing) until a cold tier exists.
SELECT add_retention_policy('usage_events', drop_after => interval '13 months');

-- Hourly usage per tenant and metric, for quotas, billing periods and reports.
-- A late, changed or deleted event invalidates its hour, and the refresh
-- policy recomputes only invalidated hours; start_offset NULL lets it reach
-- back over the whole retention.
CREATE MATERIALIZED VIEW usage_hourly WITH (timescaledb.continuous) AS
SELECT tenant_id,
       metric,
       time_bucket(interval '1 hour', event_time) AS hour,
       sum(quantity)::bigint AS quantity,
       count(*) AS events
FROM usage_events
GROUP BY tenant_id, metric, hour
WITH NO DATA;

REVOKE ALL ON usage_hourly FROM PUBLIC;

SELECT add_continuous_aggregate_policy('usage_hourly',
    start_offset => NULL,
    end_offset => interval '1 hour',
    schedule_interval => interval '5 minutes');
SELECT add_retention_policy('usage_hourly', drop_after => interval '13 months');

-- The aggregate of the session's tenant: RLS does not apply to continuous
-- aggregates, so this view is the only way in (ADR-0008).
CREATE VIEW usage_hourly_tenant WITH (security_barrier) AS
SELECT tenant_id, metric, hour, quantity, events
FROM usage_hourly
WHERE tenant_id = current_setting('app.current_tenant_id', true);

REVOKE ALL ON usage_hourly_tenant FROM PUBLIC;
GRANT SELECT ON usage_hourly_tenant TO usage_reader;
