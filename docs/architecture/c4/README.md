# C4 Model

[`workspace.dsl`](workspace.dsl) is the source of truth. The diagrams below are generated from it by [`render.sh`](render.sh); do not edit them by hand.

| View | Answers |
|---|---|
| System Context (L1) | Who uses HAMSP and which external systems it depends on. |
| RequestPath (L2) | How a request reaches tenant data, and how tenants are kept apart. |
| UsagePipeline (L2) | How a usage event becomes a stored record and is read back. |

Infrastructure (namespaces, node pools, Karpenter, KEDA, Cilium) belongs to a Deployment view, which is not drawn yet.

**Legend:** dark blue = person, blue = HAMSP, grey = external system, green = test service, dashed grey = placeholder (technology not chosen, see the issue in brackets), dashed and faded = planned for v2.

## Diagrams

<!-- diagrams:start -->

### System Context (L1)

```mermaid
graph LR
  linkStyle default fill:#ffffff

  subgraph diagram ["System Context View: HAMSP"]
    style diagram fill:#ffffff,stroke:#ffffff

    1["<div style='font-weight: bold'>Platform Admin</div><div style='font-size: 70%; margin-top: 0px'>[Person]</div><div style='font-size: 80%; margin-top:10px'>Creates, suspends and deletes<br />tenants (FR-01).</div>"]
    style 1 fill:#08427b,stroke:#052e56,color:#ffffff
    2["<div style='font-weight: bold'>Tenant Admin</div><div style='font-size: 70%; margin-top: 0px'>[Person]</div><div style='font-size: 80%; margin-top:10px'>Manages the tenant's users,<br />roles, SSO and login branding<br />(FR-02).</div>"]
    style 2 fill:#08427b,stroke:#052e56,color:#ffffff
    21["<div style='font-weight: bold'>Customer Identity Provider</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>The tenant's own SSO, SAML or<br />OIDC (FR-02, ADR-0003).</div>"]
    style 21 fill:#8a8a8a,stroke:#606060,color:#ffffff
    22["<div style='font-weight: bold'>Kubernetes API (EKS)</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Runs tenant namespaces and<br />agent workloads (FR-01,<br />FR-04, C-01).</div>"]
    style 22 fill:#8a8a8a,stroke:#606060,color:#ffffff
    23["<div style='font-weight: bold'>AWS S3</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Stores PostgreSQL WAL and<br />backups (ADR-0001, QS-13).</div>"]
    style 23 fill:#8a8a8a,stroke:#606060,color:#ffffff
    24["<div style='font-weight: bold'>Payment Provider</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Charges tenants by usage or<br />subscription. v2 (FR-06).</div>"]
    style 24 fill:#8a8a8a,stroke:#606060,color:#ffffff
    3["<div style='font-weight: bold'>End User / API Client</div><div style='font-size: 70%; margin-top: 0px'>[Person]</div><div style='font-size: 80%; margin-top:10px'>A tenant's user or machine<br />client. Calls the platform's<br />APIs with a tenant token<br />(FR-03).</div>"]
    style 3 fill:#08427b,stroke:#052e56,color:#ffffff
    4["<div style='font-weight: bold'>HAMSP</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Logs users in, keeps tenants<br />apart, runs tenant apps and<br />agent workloads, collects<br />usage events.</div>"]
    style 4 fill:#1168bd,stroke:#0b4884,color:#ffffff

    1-- "<div>Manages tenants</div><div style='font-size: 70%'>[HTTPS]</div>" -->4
    2-- "<div>Manages users, roles and SSO</div><div style='font-size: 70%'>[HTTPS]</div>" -->4
    3-- "<div>Calls the APIs</div><div style='font-size: 70%'>[HTTPS, WSS]</div>" -->4
    4-- "<div>Delegates tenant login</div><div style='font-size: 70%'>[SAML, OIDC]</div>" -->21
    4-- "<div>Creates tenant namespaces and<br />agent pods</div><div style='font-size: 70%'>[HTTPS]</div>" -->22
    4-- "<div>Streams WAL, stores backups</div><div style='font-size: 70%'>[HTTPS]</div>" -->23
    4-. "<div>Reports usage for billing</div><div style='font-size: 70%'>[HTTPS]</div>" .->24

  end
```

### RequestPath (L2)

```mermaid
graph LR
  linkStyle default fill:#ffffff

  subgraph diagram ["Container View: HAMSP"]
    style diagram fill:#ffffff,stroke:#ffffff

    1["<div style='font-weight: bold'>Platform Admin</div><div style='font-size: 70%; margin-top: 0px'>[Person]</div><div style='font-size: 80%; margin-top:10px'>Creates, suspends and deletes<br />tenants (FR-01).</div>"]
    style 1 fill:#08427b,stroke:#052e56,color:#ffffff
    2["<div style='font-weight: bold'>Tenant Admin</div><div style='font-size: 70%; margin-top: 0px'>[Person]</div><div style='font-size: 80%; margin-top:10px'>Manages the tenant's users,<br />roles, SSO and login branding<br />(FR-02).</div>"]
    style 2 fill:#08427b,stroke:#052e56,color:#ffffff
    3["<div style='font-weight: bold'>End User / API Client</div><div style='font-size: 70%; margin-top: 0px'>[Person]</div><div style='font-size: 80%; margin-top:10px'>A tenant's user or machine<br />client. Calls the platform's<br />APIs with a tenant token<br />(FR-03).</div>"]
    style 3 fill:#08427b,stroke:#052e56,color:#ffffff
    21["<div style='font-weight: bold'>Customer Identity Provider</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>The tenant's own SSO, SAML or<br />OIDC (FR-02, ADR-0003).</div>"]
    style 21 fill:#8a8a8a,stroke:#606060,color:#ffffff
    22["<div style='font-weight: bold'>Kubernetes API (EKS)</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Runs tenant namespaces and<br />agent workloads (FR-01,<br />FR-04, C-01).</div>"]
    style 22 fill:#8a8a8a,stroke:#606060,color:#ffffff
    23["<div style='font-weight: bold'>AWS S3</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Stores PostgreSQL WAL and<br />backups (ADR-0001, QS-13).</div>"]
    style 23 fill:#8a8a8a,stroke:#606060,color:#ffffff

    subgraph 4 ["HAMSP"]
      style 4 fill:#ffffff,stroke:#0b4884,color:#0b4884

      10["<div style='font-weight: bold'>ExampleApp</div><div style='font-size: 70%; margin-top: 0px'>[Container: Go]</div><div style='font-size: 80%; margin-top:10px'>Minimal tenant service that<br />reads and writes PostgreSQL<br />per tenant. Target of the<br />load and failure tests.</div>"]
      style 10 fill:#5a9e6f,stroke:#3e6e4d,color:#ffffff
      17[("<div style='font-weight: bold'>PostgreSQL</div><div style='font-size: 70%; margin-top: 0px'>[Container: CloudNativePG]</div><div style='font-size: 80%; margin-top:10px'>Tenant and application data<br />with RLS; separate database<br />for Zitadel (ADR-0002,<br />ADR-0003).</div>")]
      style 17 fill:#438dd5,stroke:#2e6295,color:#ffffff
      20[("<div style='font-weight: bold'>Rate Limit Store</div><div style='font-size: 70%; margin-top: 0px'>[Container: Redis, fit checked in HAMSP-15]</div><div style='font-size: 80%; margin-top:10px'>Shared per-tenant rate limit<br />and quota counters (FR-07).<br />Quota counters can be rebuilt<br />from the usage store.</div>")]
      style 20 fill:#438dd5,stroke:#2e6295,color:#ffffff
      5["<div style='font-weight: bold'>Tenant Web App</div><div style='font-size: 70%; margin-top: 0px'>[Container: SPA, served via CDN]</div><div style='font-size: 80%; margin-top:10px'>UI for tenant admins and end<br />users.</div>"]
      style 5 fill:#438dd5,stroke:#2e6295,color:#ffffff
      6["<div style='font-weight: bold'>Admin Portal</div><div style='font-size: 70%; margin-top: 0px'>[Container: SPA, served via CDN]</div><div style='font-size: 80%; margin-top:10px'>UI for platform admins.</div>"]
      style 6 fill:#438dd5,stroke:#2e6295,color:#ffffff
      7["<div style='font-weight: bold'>API Gateway</div><div style='font-size: 70%; margin-top: 0px'>[Container: Envoy Proxy, behind an AWS NLB]</div><div style='font-size: 80%; margin-top:10px'>Single entry point. Validates<br />the JWT, takes tenant_id from<br />it, enforces per-tenant rate<br />limits, routes requests,<br />reports every API call as a<br />usage event (FR-03, FR-05,<br />FR-07).</div>"]
      style 7 fill:#438dd5,stroke:#2e6295,color:#ffffff
      8["<div style='font-weight: bold'>Auth Service</div><div style='font-size: 70%; margin-top: 0px'>[Container: Zitadel]</div><div style='font-size: 80%; margin-top:10px'>Logs users in and issues<br />tokens. One Organization per<br />tenant (ADR-0003).</div>"]
      style 8 fill:#438dd5,stroke:#2e6295,color:#ffffff
      9["<div style='font-weight: bold'>Tenant Management</div><div style='font-size: 70%; margin-top: 0px'>[Container: Go]</div><div style='font-size: 80%; margin-top:10px'>Creates, suspends and deletes<br />tenants with all their data<br />(FR-01).</div>"]
      style 9 fill:#438dd5,stroke:#2e6295,color:#ffffff
    end

    1-- "<div>Manages tenants</div><div style='font-size: 70%'>[HTTPS]</div>" -->6
    2-- "<div>Manages users, roles and SSO</div><div style='font-size: 70%'>[HTTPS]</div>" -->5
    3-- "<div>Uses</div><div style='font-size: 70%'>[HTTPS]</div>" -->5
    3-- "<div>Calls the APIs directly</div><div style='font-size: 70%'>[HTTPS, WSS]</div>" -->7
    5-- "<div>Calls the APIs</div><div style='font-size: 70%'>[HTTPS, WSS]</div>" -->7
    6-- "<div>Calls the admin API</div><div style='font-size: 70%'>[HTTPS]</div>" -->7
    7-- "<div>Routes login, fetches JWKS</div><div style='font-size: 70%'>[OIDC]</div>" -->8
    7-- "<div>Checks per-tenant limits</div><div style='font-size: 70%'></div>" -->20
    7-- "<div>Routes tenant admin requests</div><div style='font-size: 70%'>[HTTPS]</div>" -->9
    7-- "<div>Routes tenant requests with<br />tenant_id</div><div style='font-size: 70%'>[HTTPS]</div>" -->10
    8-- "<div>Delegates tenant login</div><div style='font-size: 70%'>[SAML, OIDC]</div>" -->21
    8-- "<div>Stores users and<br />organizations</div><div style='font-size: 70%'>[SQL]</div>" -->17
    9-- "<div>Creates the tenant's<br />Organization</div><div style='font-size: 70%'>[gRPC]</div>" -->8
    9-- "<div>Stores tenants and quotas</div><div style='font-size: 70%'>[SQL]</div>" -->17
    9-- "<div>Creates tenant namespaces and<br />quotas</div><div style='font-size: 70%'>[HTTPS]</div>" -->22
    10-- "<div>Reads and writes rows, SET<br />LOCAL tenant_id</div><div style='font-size: 70%'>[SQL, RLS]</div>" -->17
    17-- "<div>Streams WAL, stores backups</div><div style='font-size: 70%'>[Barman Cloud]</div>" -->23

  end
```

### UsagePipeline (L2)

```mermaid
graph LR
  linkStyle default fill:#ffffff

  subgraph diagram ["Container View: HAMSP"]
    style diagram fill:#ffffff,stroke:#ffffff

    24["<div style='font-weight: bold'>Payment Provider</div><div style='font-size: 70%; margin-top: 0px'>[Software System]</div><div style='font-size: 80%; margin-top:10px'>Charges tenants by usage or<br />subscription. v2 (FR-06).</div>"]
    style 24 fill:#8a8a8a,stroke:#606060,color:#ffffff

    subgraph 4 ["HAMSP"]
      style 4 fill:#ffffff,stroke:#0b4884,color:#0b4884

      11["<div style='font-weight: bold'>Agent Workload</div><div style='font-size: 70%; margin-top: 0px'>[Container: Kubernetes Pod]</div><div style='font-size: 80%; margin-top:10px'>Isolated pod that runs a<br />tenant's agent code (FR-04).<br />How it is started: HAMSP-18.</div>"]
      style 11 fill:#438dd5,stroke:#2e6295,color:#ffffff
      12["<div style='font-weight: bold'>UsageWriteAPI</div><div style='font-size: 70%; margin-top: 0px'>[Container: Go]</div><div style='font-size: 80%; margin-top:10px'>Accepts usage events from the<br />API Gateway and agent<br />workloads, enforces<br />per-tenant rate limits,<br />acknowledges events once they<br />are in Kafka (FR-05, QS-07,<br />QS-08, QS-09).</div>"]
      style 12 fill:#438dd5,stroke:#2e6295,color:#ffffff
      13["<div style='font-weight: bold'>Egress Exporter</div><div style='font-size: 70%; margin-top: 0px'>[Container: TBD (HAMSP-19)]</div><div style='font-size: 80%; margin-top:10px'>Turns network flows into<br />egress usage per tenant<br />(FR-05).</div>"]
      style 13 fill:#f2f2f2,stroke:#a9a9a9,color:#333333
      14["<div style='font-weight: bold'>Usage Writer</div><div style='font-size: 70%; margin-top: 0px'>[Container: TBD: own consumer or store connector (HAMSP-14)]</div><div style='font-size: 80%; margin-top:10px'>Consumes usage events, drops<br />duplicates by event_id,<br />writes them in batches<br />(QS-06, QS-08, QS-09,<br />HAMSP-7).</div>"]
      style 14 fill:#f2f2f2,stroke:#a9a9a9,color:#333333
      15["<div style='font-weight: bold'>Metering Query Service</div><div style='font-size: 70%; margin-top: 0px'>[Container: Go]</div><div style='font-size: 80%; margin-top:10px'>Serves usage per tenant for<br />reading (FR-05).</div>"]
      style 15 fill:#438dd5,stroke:#2e6295,color:#ffffff
      16["<div style='font-weight: bold'>Billing Service</div><div style='font-size: 70%; margin-top: 0px'>[Container: Go]</div><div style='font-size: 80%; margin-top:10px'>Invoices tenants by usage or<br />subscription (FR-06).</div>"]
      style 16 fill:#438dd5,stroke:#2e6295,color:#ffffff
      18["<div style='font-weight: bold'>Kafka</div><div style='font-size: 70%; margin-top: 0px'>[Container: Apache Kafka (Strimzi)]</div><div style='font-size: 80%; margin-top:10px'>Usage events, partitioned by<br />tenant_id (ADR-0001).</div>"]
      style 18 fill:#438dd5,stroke:#2e6295,color:#ffffff
      19[("<div style='font-weight: bold'>Usage Analytics Store</div><div style='font-size: 70%; margin-top: 0px'>[Container: TBD (HAMSP-14)]</div><div style='font-size: 80%; margin-top:10px'>Usage events per tenant, kept<br />13 months.</div>")]
      style 19 fill:#f2f2f2,stroke:#a9a9a9,color:#333333
      20[("<div style='font-weight: bold'>Rate Limit Store</div><div style='font-size: 70%; margin-top: 0px'>[Container: Redis, fit checked in HAMSP-15]</div><div style='font-size: 80%; margin-top:10px'>Shared per-tenant rate limit<br />and quota counters (FR-07).<br />Quota counters can be rebuilt<br />from the usage store.</div>")]
      style 20 fill:#438dd5,stroke:#2e6295,color:#ffffff
      7["<div style='font-weight: bold'>API Gateway</div><div style='font-size: 70%; margin-top: 0px'>[Container: Envoy Proxy, behind an AWS NLB]</div><div style='font-size: 80%; margin-top:10px'>Single entry point. Validates<br />the JWT, takes tenant_id from<br />it, enforces per-tenant rate<br />limits, routes requests,<br />reports every API call as a<br />usage event (FR-03, FR-05,<br />FR-07).</div>"]
      style 7 fill:#438dd5,stroke:#2e6295,color:#ffffff
    end

    7-- "<div>Checks per-tenant limits</div><div style='font-size: 70%'></div>" -->20
    7-- "<div>Reports API calls per tenant</div><div style='font-size: 70%'>[HTTPS]</div>" -->12
    11-- "<div>Sends token usage events,<br />tenant from pod identity</div><div style='font-size: 70%'>[HTTPS]</div>" -->12
    13-- "<div>Sends egress bytes per tenant</div><div style='font-size: 70%'>[HTTPS]</div>" -->12
    12-- "<div>Checks per-tenant limits</div><div style='font-size: 70%'></div>" -->20
    12-- "<div>Publishes usage events, key<br />tenant_id</div><div style='font-size: 70%'>[Kafka]</div>" -->18
    14-- "<div>Consumes usage events</div><div style='font-size: 70%'>[Kafka]</div>" -->18
    14-- "<div>Writes events in batches</div><div style='font-size: 70%'></div>" -->19
    14-- "<div>Updates quota counters</div><div style='font-size: 70%'></div>" -->20
    7-- "<div>Routes usage reads</div><div style='font-size: 70%'>[HTTPS]</div>" -->15
    15-- "<div>Reads usage per tenant</div><div style='font-size: 70%'></div>" -->19
    16-. "<div>Reads usage for invoices</div><div style='font-size: 70%'>[HTTPS]</div>" .->15
    16-. "<div>Reports usage for billing</div><div style='font-size: 70%'>[HTTPS]</div>" .->24

  end
```

<!-- diagrams:end -->

## Tooling

Structurizr needs Java 21+ ([binaries](https://docs.structurizr.com/binaries)), or Docker.

```bash
curl -fL -o ~/.local/lib/structurizr.war https://download.structurizr.com/structurizr.war
```

* Validate and regenerate the diagrams above: `./render.sh`. If Java 21+ is not registered with `/usr/libexec/java_home`, pass it: `JAVA=/path/to/java ./render.sh`.
* Browse and lay out views at http://localhost:8080: `java -jar ~/.local/lib/structurizr.war local docs/architecture/c4`.
* Docker instead of Java: `docker run -it --rm -p 8080:8080 -v "$PWD/docs/architecture/c4:/usr/local/structurizr" structurizr/structurizr local`.
