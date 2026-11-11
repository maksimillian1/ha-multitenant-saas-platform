# Tenant states

How the API Gateway learns that a tenant is suspended or deleted (ADR-0012, item 5; HAMSP-52). The format is [`record.v1.json`](record.v1.json); this page holds only what it cannot say.

## Topic

`tenant-states` on the `usage-kafka` cluster, compacted: the latest record per key is kept forever. Key: `tenant_id` as UTF-8 bytes, so one tenant's records stay in order on one partition.

| Participant | Role |
|---|---|
| Tenant Management (`apps/tenants/events`) | Only producer. Writes after every operation and, once a minute, the state of every tenant, deleted ones included. |
| API Gateway (`apps/gateway/tenantstate`) | Reads the whole topic from the start in every replica, without a consumer group. |

Only these two can reach the brokers (the Kafka listener's network policy; authentication and ACLs are HAMSP-31).

## Rules the schema cannot express

* `tenant_id` in the value equals the key. The Gateway skips a record where they differ.
* The latest record wins. `at` is informational; readers do not reorder by it.
* The Gateway blocks `suspended`, `deleting` and `deleted`. Every other state, and a tenant it has never seen, passes.
* A reader ignores a state it does not know and keeps the tenant's earlier state. A new state is added to the readers before Tenant Management writes it.
* A change that is not additive goes to a new topic, not a new version in this one: a compacted topic keeps old records forever.
