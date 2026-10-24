# High-Availability Multi-Tenant SaaS Infrastructure Platform

Enterprise-grade, high-availability infrastructure boilerplate designed for B2B SaaS applications. Engineered for zero data loss (RPO=0), rapid automated failover (RTO < 5s), and strict multi-tenant isolation under heavy synthetic loads (up to 20 000 RPS).

---

## Core Architecture

See [Architecture Diagrams](docs/index.md#data-flow-diagram) and the [C4 model](docs/architecture/c4/README.md).

### Key constraints:
See [Requirements & Constraints](docs/architecture/requirements.md) (FR, QS, C).

### Architectural Deep-Dives (ADR Log)
For specific design justifications, performance baselines, and cost optimization trade-offs, consult the official Architecture Decision Records matching the current repository state:

* [ADR-0001: System Boundaries & Multi-Tenant Core Components](./docs/adr/0001-system-boundaries.md)
* [ADR-0002: Data Isolation Strategy for Multi-Tenant Architecture](./docs/adr/0002-data-isolation-strategy.md)
* [ADR-0003: Auth Service Implementation (Zitadel)](./docs/adr/0003-auth-service-implementation.md)
* [ADR-0004: API Gateway as a Go service behind Cilium Gateway API](./docs/adr/0004-api-gateway-go-service.md)

---

## Directory Structure
See [Repository Structure](docs/index.md#directory-structure).

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
