# ADR-0005: Kafka runs on Strimzi inside EKS

Date: 2026-10-24

## Status

Accepted

Details how the Kafka of [ADR-0001](0001-system-boundaries.md), item 4, runs. Decided in HAMSP-8.

## Context

Kafka itself is fixed (C-05), and since Kafka 4.0 KRaft is its only mode, so neither is a choice. The choice is how Kafka runs. It carries the usage events between UsageWriteAPI and Usage Writer (ADR-0001, item 4).

What it must handle:

* **Load:** up to 20 000 events/s at peak, about 10 MB/s at ~0.5 KB per event (Sizing in `docs/architecture/requirements.md`). A small cluster handles this; throughput does not decide the option.
* **No loss of acknowledged events:** losing one broker (pod deleted or node lost) loses no event that got a 2xx (QS-17), and Kafka buffers events while the usage store is down (QS-09).
* **Scaling on consumer lag** with KEDA (QS-07, QS-08).
* **Budget and teardown:** fits the monthly budget and the environment can be torn down when not in use (C-02, QS-14).
* **GitOps:** created only through Terraform and ArgoCD (C-04).
* **Local run:** the prototype is a kind cluster (`/prototype`), and the next skeleton step, the usage pipeline (HAMSP-23), needs Kafka there.
* **Operations:** one person runs the platform.

We compared **Strimzi** (operator inside EKS), **Amazon MSK** (managed) and **Redpanda** (Kafka API compatible).

## Decision

We run Kafka with the **Strimzi** operator inside our EKS cluster, and with the same operator on the local kind cluster.

**Why Strimzi:**

* **Same setup on kind and in EKS:** the same operator and custom resources, deployed by ArgoCD like CNPG (C-04). Only sizes and storage differ.
* **Cheapest:** we pay only for the EC2 nodes and EBS volumes the brokers use.
* **QS-17 can be tested as written:** Chaos Mesh deletes a broker pod or drains its node.
* **Teardown in minutes,** together with the cluster.
* Topics and users are custom resources (`KafkaTopic`, `KafkaUser`) in Git. KEDA's Kafka scaler reads consumer lag directly.
* The same operator model as CNPG for PostgreSQL. Strimzi is a CNCF project under Apache 2.0.

**Why not Amazon MSK:**

* It does not run locally, so dev would need another Kafka and would drift from production.
* A broker costs more than an EC2 instance of the same size. MSK Serverless is cheaper to start with, but it allows only IAM authentication, which ties every client to the AWS SDK.
* Creating a cluster takes tens of minutes, which slows down the teardown and recreate cycle of C-02.
* QS-17 cannot be tested as written: a broker cannot be deleted, only rebooted through the AWS API.

**Why not Redpanda:**

* It is not Apache Kafka, and C-05 fixes Kafka.
* Its free edition is source-available (BSL), not open source.

## How we run it

The first Kafka cluster arrives with the usage pipeline skeleton, which pins the versions and writes the manifests. These rules apply to it.

1. **Operator and cluster:** Strimzi from its Helm chart, deployed by ArgoCD, with one `Kafka` cluster in namespace `kafka`. KRaft only, with nodes defined in `KafkaNodePool`s. EKS starts with 3 nodes that are both controller and broker; we split the roles only if load requires it.
2. **Durability (QS-17):**
   * One broker per availability zone, with rack awareness on `topology.kubernetes.io/zone`.
   * `default.replication.factor=3`, `min.insync.replicas=2`, `unclean.leader.election.enable=false`.
   * UsageWriteAPI produces with `acks=all` and an idempotent producer, and answers 2xx only after Kafka confirms the write (ADR-0001, item 4).
   * A topic stays writable with one broker down; with two brokers down producers get errors instead of losing data.
3. **Nodes:**
   * Kafka never runs on Spot. It gets a dedicated on-demand Karpenter NodePool with a taint that only Kafka pods tolerate.
   * Kafka pods carry `karpenter.sh/do-not-disrupt`, so consolidation does not evict them.
   * Strimzi's PodDisruptionBudget lets one broker go down at a time.
4. **Storage:** persistent EBS gp3 volumes. A volume is bound to its AZ, which matches one broker per AZ.
5. **Scaling:** KEDA scales Usage Writer on its consumer group lag (QS-07, QS-08). Adding brokers is a manual change in Git, followed by a Cruise Control rebalance (`KafkaRebalance`).
6. **Local (kind):** the same operator with smaller resources and local storage. The kind cluster has two workers, so it cannot reproduce three AZs; QS-17 is proven on EKS.

## Consequences

**Good:**

* One way to run Kafka from laptop to EKS, all in Git.
* The lowest running cost of the three options, and the environment can be torn down with the cluster.
* Failure tests (QS-09, QS-17) are ordinary Chaos Mesh experiments.

**Bad / risks:**

* **We operate Kafka ourselves:** Strimzi and Kafka upgrades, disk sizing and expansion, broker replacement, rebalancing. That is time the person running the platform does not spend on features.
* Three on-demand nodes run all the time while the environment is up; they are a fixed part of the C-02 budget.
* Losing an AZ removes a third of the brokers. Topics stay writable, but the cluster runs without spare capacity until the AZ is back.
* Monitoring is ours: Strimzi's Prometheus metrics, alerts on under-replicated partitions, disk usage and consumer lag.
* The cost comparison is qualitative. Before the first EKS environment, the broker nodes and volumes must be priced for `eu-central-1` against the C-02 budget, which is still `TODO`.
