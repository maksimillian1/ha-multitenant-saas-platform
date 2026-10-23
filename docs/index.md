# High-Availability Multi-Tenant SaaS Platform (HAMSP)

HAMSP is an infrastructure boilerplate for B2B SaaS products in which many customer companies (tenants)
share one Kubernetes cluster and one database.
It logs users in, keeps tenants apart and takes in usage events from tenant apps and agent workloads. Usage-based billing is planned for v2.

---

## Master Navigation Hub

Every phase MUST also read [Requirements & Constraints](architecture/requirements.md) (FR, QS, C). The `verify` phase checks the work against its quality scenarios (QS-xx).

Specific technical constraints must be sourced from the specialized files below:
- [Data Tier (Zero Data Loss, RPO=0, RLS)](architecture/data-tier.md)
- [Ingestion & Telemetry Pipeline (Async Load Leveling)](architecture/ingestion-pipeline.md)
- [Networking & Security (Hard Multi-Tenant Isolation, Cilium)](architecture/networking-security.md)
- [Control Plane (Automated Recovery, RTO < 5s)](architecture/control-plane.md)
- [Operations & Infrastructure (Terraform)](ops/terraform.md)
- [Contracts](contracts/)
- [C4 model (Structurizr DSL)](architecture/c4/workspace.dsl): System Context and two Container views (RequestPath, UsagePipeline)

**Override Rule**: Accepted ADRs in [adr/](adr/) hold absolute priority over all other architectural documentation.

The ADR folder is the decision log. When an ADR changes something important in a subsystem, the subsystem file links to that ADR.

---

<h2 id="data-flow-diagram">Container and Data Flow Diagram</h2>

> **Note:** the diagram does not match ADR-0001 yet (tenant namespaces vs one shared API, Gateway vs ALB/NLB). See HAMSP-11.

```mermaid
graph TB
%% External Layer & Users
subgraph Clients_and_Portals [Client Space]
TenantAdmin[Tenant Admin] -->|Manage App| FrontApp[Frontend App - Served via CDN]
RootAdmin[System Root Admin] -->|Manage Platform| FrontAdmin[Admin Portal - Served via CDN]
EndUser[End User / Agent API Client] -->|API Requests| FrontApp
end

    %% Routing & Security Layer (Cilium & Gateway API Powered)
    subgraph Routing_and_Security_Layer [Routing & Security - System Namespace]
        FrontApp -->|HTTPS/WSS| K8sGateway[K8s Gateway - Cilium / Envoy]
        FrontAdmin -->|HTTPS| K8sGateway
        
        %% Cilium eBPF Network Controls
        Cilium[Cilium CNI with eBPF] -.->|Enforce NetworkPolicies| K8sGateway
        Cilium -.->|L7 Traffic Telemetry| eBPFCollector[Cilium Hubble / Prometheus]
    end

    %% Control Plane (System Namespace - Platform Core)
    subgraph Control_Plane [Control Plane - System Namespace]
        K8sGateway -->|Auth Requests| AuthService[Auth Service]
        K8sGateway -->|Tenant CRUD| TenantMgmt[Tenant Management Service]
        K8sGateway -->|Invoicing & Subscription| BillingService[Billing Service]
        K8sGateway -->|Usage Analytics API| MeteringQueryService[Metering Query Service]
        
        TenantMgmt -->|Control Loop| K8sAPI[Kubernetes API Server]
        K8sAPI -->|Auto-scale Nodes| Karpenter[Karpenter Node Autoscaler]
    end

    %% Data Plane (Dynamic Tenant Namespaces - Hard Multi-Tenancy)
    subgraph Data_Plane [Data Plane - Isolated Tenant Namespaces]
        
        %% Tenant A Namespace
        subgraph Tenant_A_Namespace [Tenant A Namespace]
            K8sGateway -.->|Route via Tenant A HTTPRoute| TenantA_App[Tenant A App Pod]
            TenantA_App -->|Spawn Agent Pods| K8sAPI
            
            %% Ephemeral Execution Sandbox
            AgentPodA[Isolated Agent Pod - Tenant A]
            
            %% KEDA Autoscaling for Tenant Workloads
            KEDA_A[KEDA Scaler] -.->|Scale App Pods| TenantA_App
        end

        %% Tenant B Namespace
        subgraph Tenant_B_Namespace [Tenant B Namespace]
            K8sGateway -.->|Route via Tenant B HTTPRoute| TenantB_App[Tenant B App Pod]
            KEDA_B[KEDA Scaler] -.->|Scale App Pods| TenantB_App
        end
    end

    %% Ingestion & Metering Pipeline (Async High-Volume FinOps)
    subgraph Metering_Pipeline [FinOps & Ingestion Pipeline]
        %% App level metrics (Tokens, API hits)
        TenantA_App -->|App Metrics Event| Kafka[(Apache Kafka Cluster)]
        AgentPodA -->|Token Usage Event| Kafka
        
        %% Network level metrics (eBPF telemetry)
        eBPFCollector -->|Bytes/Egress Telemetry| Kafka
        
        %% Processing Layer
        Kafka -->|Batch Read| MeteringIngestion[Metering Ingestion Worker]
        MeteringIngestion -->|Dedup by event_id, Bulk Write| UsageStore[(Usage Analytics Store - TBD)]
    end

    %% Shared Storage & Core Databases
    subgraph Shared_Storage [Shared State & DBs - System Namespace]
        AuthService --> DB[(PostgreSQL + CloudNativePG)]
        TenantMgmt --> DB
        BillingService --> DB
        
        AuthService -.->|Cache Tokens| TokenCache[(Token Cache - TBD)]
        MeteringIngestion -.->|Sync Real-time Limits| LimitStore[(Rate Limit and Quota Store - Redis)]
        K8sGateway -.->|Fast-path Limit Check| LimitStore
    end

    %% Async Communication Bridges
    UsageStore -.->|Query Raw Aggregates| MeteringQueryService
    MeteringQueryService -.->|Billing Calculations| BillingService

    %% Placeholders: technology not chosen yet (HAMSP-14, HAMSP-15)
    classDef placeholder stroke-dasharray: 5 5;
    class UsageStore,TokenCache placeholder
```

---

<h2 id="directory-structure">Repository Structure</h2>
* `/terraform` - Infrastructure as Code for AWS EKS, Karpenter, and core cloud dependencies.
* `/k8s` - Kubernetes manifests, Helm charts, GitOps configuration (ArgoCD), and Cilium policies.
* `/docs/adr` - Architecture Decision Records (ADR) detailing design trade-offs.
* `/apps` - Core Go/Node.js high-throughput API implementation.
