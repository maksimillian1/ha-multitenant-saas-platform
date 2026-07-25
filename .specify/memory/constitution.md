<!--
Sync Impact Report:
- Version change: [CONSTITUTION_VERSION] -> 1.0.0
- Modified principles:
  - Added: I. Docs Hierarchy (Master Navigation Hub)
  - Added: II. Zero Data Loss (RPO=0)
  - Added: III. Automated Recovery (RTO < 5s)
  - Added: IV. Hard Multi-Tenant Isolation
  - Added: V. Asynchronous Load Leveling
- Added sections: None
- Removed sections: Sections 2 and 3 from template
- Templates requiring updates:
  - ✅ .specify/templates/plan-template.md
  - ✅ .specify/templates/spec-template.md
  - ✅ .specify/templates/tasks-template.md
- Follow-up TODOs: None
-->

# HA Multi-Tenant SaaS Platform Constitution

## Core Principles

### I. Docs Hierarchy (Master Navigation Hub)
The root file `docs/index.md` is the Master Navigation Hub. Any Software Design Document (SDD) phase (specify, plan, tasks, implement, verify) MUST begin by reading this file to determine the affected subsystem. Specific technical constraints MUST be sourced from specialized files (`docs/architecture/data-tier.md`, `docs/architecture/ingestion-pipeline.md`, `docs/architecture/networking-security.md`, `docs/architecture/control-plane.md`, `docs/ops/terraform.md`, `docs/contracts/`). Accepted ADRs in `docs/adr/` hold absolute priority and MUST override other documentation.

### II. Zero Data Loss (RPO=0)
Database writes MUST strictly utilize synchronous replication and WAL-streaming to AWS S3 via Barman Cloud to guarantee a Recovery Point Objective of zero (RPO=0).

### III. Automated Recovery (RTO < 5s)
System recovery MUST be fully automated with a Recovery Time Objective of under 5 seconds (RTO < 5s). This MUST be implemented using CloudNativePG and native Kubernetes mechanisms without manual intervention.

### IV. Hard Multi-Tenant Isolation
Strict multi-tenant isolation MUST be enforced across tiers. 
- **Data Tier:** `ENABLE ROW LEVEL SECURITY` MUST be applied to all multi-tenant tables, and session context (`app.current_tenant_id`) MUST be injected before transactions.
- **Network Tier:** Cross-tenant traffic MUST be mutually prohibited via `CiliumNetworkPolicy` (eBPF).

### V. Asynchronous Load Leveling
Peak loads (e.g., webhooks) MUST be offloaded asynchronously to Apache Kafka (KRaft) and scaled by workers using KEDA lag metrics. Direct writes of peak loads to the database are strictly prohibited.

## Governance

The constitution supersedes all other practices. Amendments require documentation, approval, and a migration plan. All PRs and reviews MUST verify compliance with these principles.

**Version**: 1.0.0 | **Ratified**: 2026-07-25 | **Last Amended**: 2026-07-25
