# ADR-0013: Business apps join the platform through a contract, deployed from a registry

Date: 2026-11-25

## Status

Proposed

Decided in HAMSP-96, part of HAMSP-95. Keeps [ADR-0002](0002-data-isolation-strategy.md) (RLS in the shared database), [ADR-0004](0004-api-gateway-go-service.md) and the [header contract](../contracts/gateway-headers.md) (the Gateway sets `x-tenant-id`), [ADR-0010](0010-usage-event-contracts.md) (usage events) and [ADR-0012](0012-tenant-provisioning-lifecycle.md) (Tenant Management owns the tenant lifecycle). The guide for app authors is [INTEGRATION.md](../INTEGRATION.md).

## Context

HAMSP is a boilerplate for B2B SaaS products. So far its only "products" are test services in this repository: ExampleApp and exampleweb (ADR-0001, item 2). They prove the platform works, but not how a real product is built on it.

A real product is a business application: a CRM, an LMS, a helpdesk, written by its own team or forked from an open-source project. Three ways to build one on HAMSP:

1. **Fork HAMSP for each product** and put the product's code into the fork.
2. **Install HAMSP into a cluster the product already has.**
3. **Run HAMSP as a platform and connect products to it** through a fixed contract.

Option 1 copies the platform into every product. A fix to the Gateway, RLS or metering has to be carried into every fork, and tenant isolation stops being a property of the platform and becomes a property of each fork. Option 2 needs HAMSP to run with any CNI, Postgres, Kafka and IdP the cluster already has; its isolation depends on Cilium (the header contract) and CNPG, so in a foreign cluster it cannot keep its guarantees.

Constraints that shape option 3:

* C-03: nothing under `/apps` may depend on AGPL code. Many open-source business apps are AGPL, so they cannot live in this repository.
* C-04: no manual changes in the cluster. Connecting an app cannot be a `kubectl apply`.
* An app is code the platform owner does not review line by line. It must not be able to weaken tenant isolation, whether by mistake or on purpose.

## Decision

1. **Two lifecycles.** The platform and every business app have their own repository, their own version and their own release cycle. They meet only at the contract (item 2). The platform is upgraded without touching the apps, and an app is deployed without touching the platform. No product forks HAMSP.

2. **The contract.** What the platform gives an app and what the app must do:

   | Area | The platform gives | The app does |
   |---|---|---|
   | Sign-in | Users signed in with Zitadel; the request reaches the app with `x-tenant-id` and the user's headers, with no token ([contract](../contracts/gateway-headers.md); browser sessions are HAMSP-100) | Has no sign-in of its own; trusts only these headers |
   | Data | A database in CNPG and a role without `BYPASSRLS` that does not own the tables; an owner role for migrations | Has `tenant_id` on every tenant table, enables and forces RLS, runs `set_config('app.current_tenant_id', …, true)` in every transaction, answers 404 for another tenant's record |
   | Tenants | Tenant events from Tenant Management (ADR-0012; contract in HAMSP-101) | Creates a tenant's data when it appears and deletes it when the tenant is deleted |
   | Usage | A Kafka topic and a user for it | Sends usage events in the ADR-0010 schema |
   | Network | A namespace that only the Gateway can reach | Listens on one HTTP port; calls nothing it did not declare |
   | Deployment | A library Helm chart (item 4), an Argo CD Application, a namespace | Ships an image (multi-arch, C-06) and a chart built on the library chart |

3. **An app is connected by a registry entry, not by a manual step.** The HAMSP repository holds a registry of business apps: one entry per app with its repository, path, revision and namespace. An ApplicationSet creates an Argo CD Application from each entry. Adding an app is a pull request to the registry; removing it is a pull request too. Every app gets:
   * a namespace of its own, `app-<name>`;
   * an AppProject that allows only that namespace and a whitelist of resource kinds (Deployment, Service, ConfigMap, Secret, Job, the CRD kinds the library chart renders) and denies everything else, network policies included.

   HAMSP-97 builds it.

4. **A library Helm chart renders the platform resources.** An app's chart depends on a versioned library chart from HAMSP. The app declares what it needs in its values (a database, topics, a route), and the library chart renders the third-party resources: the CNPG `Database`, Strimzi `KafkaTopic` and `KafkaUser`, the route, standard env, probes, resources and labels. Apps do not write these CRDs themselves. When the platform changes or replaces one of the operators, the library chart changes, not every app. HAMSP-98 builds it.

5. **Isolation belongs to the platform, and the cluster enforces it.** Documentation tells an app author what is forbidden; it does not stop anyone. The controls an app could use to break tenant isolation are owned by the platform and enforced in three layers:
   * **Argo CD:** the AppProject denies `CiliumNetworkPolicy`, `CiliumClusterwideNetworkPolicy` and `NetworkPolicy`.
   * **Admission:** a Kyverno rule rejects these kinds in `app-*` namespaces from anyone but the platform, which closes the path around Argo CD (Helm or `kubectl` by hand).
   * **Cilium:** a cluster-wide policy of the platform denies traffic into `app-*` namespaces from every source but the API Gateway (and the kubelet's health checks). Cilium deny rules win over allow rules, so an allow policy that appears anyway cannot open the app.

   The app's database role is created by the platform (CNPG managed roles, owned by the platform), never by the app's chart. The Zitadel project and roles of an app are created by the platform's Terraform. HAMSP-99 builds these controls; it is a critical feature.

6. **Platform upgrades must not break an app's next deploy.** A running app rarely breaks when an operator is upgraded: Kubernetes converts stored resources between API versions. What breaks is the next sync of an app whose chart uses a version that was removed. So:
   * HAMSP is released with semantic versions. A removed API version or a removed library chart value is a major release, after a period in which both work.
   * Before a platform release, CI renders the chart of every registered app with the new library chart and validates the output against the CRDs of the new platform (`kubeconform`).
   * An upgrade goes in this order: the library chart supports both versions, the apps move to it, then the operators in the cluster are upgraded.

   HAMSP-102 adds the CI check.

7. **The platform checks every connected app.** The tenant isolation tests T-01, T-04 and T-12 ([Testing](../process/testing.md), section 4) run against each registered app after it is deployed, from the platform, not from the app's own repository. An app that fails them is not ready. HAMSP-102 builds them.

8. **ExampleApp and exampleweb stay where they are.** They are the platform's own test services (ADR-0001, item 2) and keep living under `/apps`. They are not business apps and do not go through the registry. A business app lives in its own repository; an AGPL fork must (C-03). The first one is the showcase app (HAMSP-103, [ADR-0014](0014-showcase-app.md)).

9. **No operator yet.** Items 3 to 7 use Argo CD, a library chart, Kyverno and existing CRDs. An operator of our own, reading one `HamspApp` resource per app and creating the database, roles, route, topics, Zitadel project and policies from it, is the target state. It comes when one of these holds:
   * there are enough apps that the per-app registry, AppProject and Terraform steps become routine work;
   * the platform wants to hide CNPG and Strimzi from apps entirely;
   * an app needs something the existing CRDs cannot express, such as a Zitadel project created on deploy.

   The move needs no change in the apps: the library chart starts rendering `HamspApp` instead of the third-party resources.

## Considered options

| Option | Verdict |
|---|---|
| Fork HAMSP for each product | Rejected. Every fix goes into every fork; isolation becomes a per-fork property. |
| HAMSP installs into any existing cluster | Not now. Isolation depends on Cilium and CNPG; a foreign cluster cannot keep the guarantees. Packaging HAMSP for a cluster with stated requirements stays possible later. |
| The app's own Terraform imports a HAMSP module | Rejected. The app learns the platform's internals, and every change of the module needs a change in every app. |
| The app holds an Argo CD Application that is applied by hand once | Rejected. A manual step in the cluster (C-04), with no record in git; whoever applies it needs rights in the `argocd` namespace. The registry entry gives the same pull from the app's repository through a pull request. |
| An operator with a `HamspApp` resource now | Not now (item 9). More code to write and run before the contract has been tried on one real app. |
| Crossplane compositions or Kyverno generate rules instead of the library chart | Not now. Less Go code, but the logic moves into YAML that is hard to test; a library chart is enough for one or two apps. A possible step between the chart and the operator. |
| The app writes its own network policies | Rejected. Cilium policies add up; an app could open itself to any pod and let it forge `x-tenant-id`. |

## Consequences

**Good:**

* One platform, many products. A fix in the platform reaches every app at once.
* Tenant isolation stays a property of the platform: network, database roles and the conformance tests are not in the app's hands.
* An AGPL application can run on the platform without touching C-03: it lives in its own repository and talks to the platform over HTTP and Kafka.
* Connecting and removing an app are pull requests, reviewed and reversible.

**Bad / risks:**

* **New components.** Kyverno and `kubeconform` are not in the stack yet. Kyverno is an admission webhook in the path of every write to the cluster; it needs HA and a failure policy.
* **Browser sign-in is not solved yet.** The Gateway accepts only bearer tokens (ADR-0004), and business apps have a browser UI. Until HAMSP-100 decides between a session mode in the Gateway and a shared BFF, the contract's sign-in row is a target, not a fact.
* **The library chart is a contract too.** Its values become an API that apps depend on. Changing it carelessly breaks every app at its next deploy; item 6 is the protection.
* **Apps still see CNPG and Strimzi** in their rendered manifests and in their status. Until the operator exists, a platform change in those operators is visible to app teams.
* **An app's own isolation is still the app's code.** The platform forces RLS to exist and tests it from outside, but a forked app with its own data paths (a cache, files, background jobs) can leak data in ways RLS does not see. The conformance tests and the app's own review are the protection.
