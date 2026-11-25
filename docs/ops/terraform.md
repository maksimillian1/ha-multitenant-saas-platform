# Operations & Infrastructure (Terraform)

[Template: Populate with Infrastructure as Code guidelines, EKS, Karpenter configs, and operational requirements.]

Node pools are arm64 (Graviton) first (C-06 in [requirements](../architecture/requirements.md)). Spot pools may allow both architectures, because every image we build is multi-arch.
