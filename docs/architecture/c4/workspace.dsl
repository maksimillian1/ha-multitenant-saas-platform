/*
 * HAMSP C4 model: one model, several views.
 *
 * Tooling: https://docs.structurizr.com/binaries (structurizr.war needs Java 21+, or use the Docker image).
 *
 * Validate:
 *   java -jar structurizr.war validate -workspace docs/architecture/c4/workspace.dsl
 *   docker run --rm -v "$PWD/docs/architecture/c4:/usr/local/structurizr" structurizr/structurizr validate -workspace /usr/local/structurizr/workspace.dsl
 * View locally (http://localhost:8080):
 *   java -jar structurizr.war local docs/architecture/c4
 *   docker run -it --rm -p 8080:8080 -v "$PWD/docs/architecture/c4:/usr/local/structurizr" structurizr/structurizr local
 * Export to Mermaid:
 *   java -jar structurizr.war export -workspace docs/architecture/c4/workspace.dsl -format mermaid -output <dir>
 *
 * Tags:
 *   External     - outside the HAMSP boundary.
 *   Planned      - not in v1 scope.
 *   Placeholder  - technology not chosen yet; the element names the role, not the product.
 *   Test         - exists to exercise the platform, not part of the product.
 */
workspace "HAMSP" "High-Availability Multi-Tenant SaaS Platform" {

    !identifiers hierarchical

    model {
        platformAdmin = person "Platform Admin" "Creates, suspends and deletes tenants (FR-01)."
        tenantAdmin = person "Tenant Admin" "Manages the tenant's users, roles, SSO and login branding (FR-02)."
        endUser = person "End User / API Client" "A tenant's user or machine client. Calls the platform's APIs with a tenant token (FR-03)."

        hamsp = softwareSystem "HAMSP" "Logs users in, keeps tenants apart, runs tenant apps and agent workloads, collects usage events." {
            tenantWeb = container "Tenant Web App" "UI for tenant admins and end users." "SPA, served via CDN" "Web Browser"
            adminPortal = container "Admin Portal" "UI for platform admins." "SPA, served via CDN" "Web Browser"

            apiGateway = container "API Gateway" "Single entry point. Validates the JWT, takes tenant_id from it, enforces per-tenant rate limits, routes requests, reports every API call as a usage event (FR-03, FR-05, FR-07)." "Envoy Proxy, behind an AWS NLB"
            authService = container "Auth Service" "Logs users in and issues tokens. One Organization per tenant (ADR-0003)." "Zitadel"
            tenantMgmt = container "Tenant Management" "Creates, suspends and deletes tenants with all their data (FR-01)." "Go"
            exampleApp = container "ExampleApp" "Minimal tenant service that reads and writes PostgreSQL per tenant. Target of the load and failure tests." "Go" "Test"
            agentWorkload = container "Agent Workload" "Isolated pod that runs a tenant's agent code (FR-04). How it is started: HAMSP-18." "Kubernetes Pod"

            usageWriteApi = container "UsageWriteAPI" "Accepts usage events from the API Gateway and agent workloads, enforces per-tenant rate limits, acknowledges events once they are in Kafka (FR-05, QS-07, QS-08, QS-09)." "Go"
            egressExporter = container "Egress Exporter" "Turns network flows into egress usage per tenant (FR-05)." "TBD (HAMSP-19)" "Placeholder"
            usageWriter = container "Usage Writer" "Consumes usage events, drops duplicates by event_id, writes them in batches (QS-06, QS-08, QS-09, HAMSP-7)." "Go"
            meteringQuery = container "Metering Query Service" "Serves usage per tenant for reading (FR-05)." "Go"
            billing = container "Billing Service" "Invoices tenants by usage or subscription (FR-06)." "Go" "Planned"

            postgres = container "PostgreSQL" "Tenant and application data with RLS; separate database for Zitadel (ADR-0002, ADR-0003)." "CloudNativePG" "Database"
            kafka = container "Kafka" "Usage events, partitioned by tenant_id (ADR-0001)." "Apache Kafka (Strimzi)" "Queue"
            usageStore = container "Usage Store" "Usage events per tenant, kept 13 months: hot and warm tiers in one hypertable, hourly aggregate. Platform-only (ADR-0006, ADR-0008)." "TimescaleDB on its own CNPG cluster; cold tier in S3 in v2" "Database"
            rateLimitStore = container "Rate Limit Store" "Shared per-tenant rate limit and quota counters (FR-07). Quota counters can be rebuilt from the usage store." "Redis, fit checked in HAMSP-15" "Database"
        }

        customerIdp = softwareSystem "Customer Identity Provider" "The tenant's own SSO, SAML or OIDC (FR-02, ADR-0003)." "External"
        kubernetes = softwareSystem "Kubernetes API (EKS)" "Runs tenant namespaces and agent workloads (FR-01, FR-04, C-01)." "External"
        s3 = softwareSystem "AWS S3" "Stores PostgreSQL WAL and backups (ADR-0001, QS-13)." "External"
        payments = softwareSystem "Payment Provider" "Charges tenants by usage or subscription. v2 (FR-06)." "External,Planned"

        # System context
        platformAdmin -> hamsp "Manages tenants" "HTTPS"
        tenantAdmin -> hamsp "Manages users, roles and SSO" "HTTPS"
        endUser -> hamsp "Calls the APIs" "HTTPS, WSS"
        hamsp -> customerIdp "Delegates tenant login" "SAML, OIDC"
        hamsp -> kubernetes "Creates tenant namespaces and agent pods" "HTTPS"
        hamsp -> s3 "Streams WAL, stores backups" "HTTPS"
        hamsp -> payments "Reports usage for billing" "HTTPS" "Planned"

        # Request path
        platformAdmin -> hamsp.adminPortal "Manages tenants" "HTTPS"
        tenantAdmin -> hamsp.tenantWeb "Manages users, roles and SSO" "HTTPS"
        endUser -> hamsp.tenantWeb "Uses" "HTTPS"
        endUser -> hamsp.apiGateway "Calls the APIs directly" "HTTPS, WSS"
        hamsp.tenantWeb -> hamsp.apiGateway "Calls the APIs" "HTTPS, WSS"
        hamsp.adminPortal -> hamsp.apiGateway "Calls the admin API" "HTTPS"

        hamsp.apiGateway -> hamsp.authService "Routes login, fetches JWKS" "OIDC"
        hamsp.apiGateway -> hamsp.rateLimitStore "Checks per-tenant limits"
        hamsp.apiGateway -> hamsp.tenantMgmt "Routes tenant admin requests" "HTTPS"
        hamsp.apiGateway -> hamsp.exampleApp "Routes tenant requests with tenant_id" "HTTPS"

        hamsp.authService -> customerIdp "Delegates tenant login" "SAML, OIDC"
        hamsp.authService -> hamsp.postgres "Stores users and organizations" "SQL"
        hamsp.tenantMgmt -> hamsp.authService "Creates the tenant's Organization" "gRPC"
        hamsp.tenantMgmt -> hamsp.postgres "Stores tenants and quotas" "SQL"
        hamsp.tenantMgmt -> kubernetes "Creates tenant namespaces and quotas" "HTTPS"
        hamsp.exampleApp -> hamsp.postgres "Reads and writes rows, SET LOCAL tenant_id" "SQL, RLS"
        hamsp.postgres -> s3 "Streams WAL, stores backups" "Barman Cloud"

        # Usage pipeline
        hamsp.apiGateway -> hamsp.usageWriteApi "Reports API calls per tenant" "HTTPS"
        hamsp.agentWorkload -> hamsp.usageWriteApi "Sends token usage events, tenant from pod identity" "HTTPS"
        hamsp.egressExporter -> hamsp.usageWriteApi "Sends egress bytes per tenant" "HTTPS"
        hamsp.usageWriteApi -> hamsp.rateLimitStore "Checks per-tenant limits"
        hamsp.usageWriteApi -> hamsp.kafka "Publishes usage events, key tenant_id" "Kafka"
        hamsp.usageWriter -> hamsp.kafka "Consumes usage events" "Kafka"
        hamsp.usageWriter -> hamsp.usageStore "Writes events in batches"
        hamsp.usageWriter -> hamsp.rateLimitStore "Updates quota counters"
        hamsp.apiGateway -> hamsp.meteringQuery "Routes usage reads" "HTTPS"
        hamsp.meteringQuery -> hamsp.usageStore "Reads usage per tenant"
        hamsp.billing -> hamsp.meteringQuery "Reads usage for invoices" "HTTPS" "Planned"
        hamsp.billing -> payments "Reports usage for billing" "HTTPS" "Planned"
    }

    views {
        systemContext hamsp "SystemContext" "L1: who uses HAMSP and which systems it depends on." {
            include *
            autoLayout lr
        }

        container hamsp "RequestPath" "L2: how a request reaches tenant data, and how tenants are kept apart." {
            include platformAdmin tenantAdmin endUser
            include hamsp.tenantWeb hamsp.adminPortal hamsp.apiGateway hamsp.authService hamsp.tenantMgmt hamsp.exampleApp
            include hamsp.postgres hamsp.rateLimitStore
            include customerIdp kubernetes s3
            autoLayout lr
        }

        container hamsp "UsagePipeline" "L2: how a usage event becomes a stored record and is read back." {
            include hamsp.agentWorkload hamsp.egressExporter
            include hamsp.apiGateway hamsp.usageWriteApi hamsp.kafka hamsp.usageWriter
            include hamsp.usageStore hamsp.rateLimitStore
            include hamsp.meteringQuery hamsp.billing payments
            autoLayout lr
        }

        styles {
            element "Element" {
                color #ffffff
            }
            element "Person" {
                shape Person
                background #08427b
            }
            element "Software System" {
                background #1168bd
            }
            element "Container" {
                background #438dd5
            }
            element "Web Browser" {
                shape WebBrowser
            }
            element "Database" {
                shape Cylinder
            }
            element "Queue" {
                shape Pipe
            }
            element "Test" {
                background #5a9e6f
            }
            element "External" {
                background #8a8a8a
            }
            element "Planned" {
                border dashed
                opacity 60
            }
            element "Placeholder" {
                border dashed
                background #f2f2f2
                color #333333
            }
            relationship "Relationship" {
                dashed false
            }
            relationship "Planned" {
                dashed true
                opacity 60
            }
        }
    }
}
