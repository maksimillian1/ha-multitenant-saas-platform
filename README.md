# High-Availability Multi-Tenant SaaS Infrastructure Platform

Enterprise-grade, high-availability infrastructure boilerplate designed for B2B SaaS applications. Engineered for zero data loss (RPO=0), rapid automated failover (RTO < 5s), and strict multi-tenant isolation under heavy synthetic loads (5000+ RPS).

---

## Core Architecture

Look [Architecture Diagram](./docs/architecture.md#data-flow-diagram)

### Key constraints:
TODO

### Architectural Deep-Dives (ADR Log)
For specific design justifications, performance baselines, and cost optimization trade-offs, consult the official Architecture Decision Records matching the current repository state:

* [ADR-0001: System Boundaries & Multi-Tenant Core Components](./docs/adr/0001-system-boundaries.md)
* [ADR-0002: Data Isolation Strategy for Multi-Tenant Architecture](./docs/adr/0002-data-isolation-strategy)

---

## Directory Structure
Look [Directory Structure](./docs/architecture.md#directory-structure)

---


## Development and ADR Tooling

Architectural changes must be peer-reviewed via the `adr-tools` standard before executing any code changes.

* **Initialize new design record:** `adr new "Your Decision Title"`
* **Supersede an existing policy:** `adr new -supersedes 0004 "Migrating Framework"`

## Setup

### Terraform
* terraform init
* terraform apply

### Kubernetes
* aws eks update-kubeconfig --region eu-central-1 --name ha-multi-tenant-saas
