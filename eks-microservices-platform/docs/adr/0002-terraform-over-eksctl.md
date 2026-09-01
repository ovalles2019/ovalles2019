# 2. Provision with Terraform, not an eksctl command in a README

Status: accepted

## Context

The common starting point for an EKS project — and what the reference project
this one improves on does — is a command pasted into a README:

```
eksctl create cluster --name enterprise-cluster --region us-east-1 \
  --nodegroup-name standard-workers --node-type t3.medium --nodes 3 --managed
```

It works. It also has no state file, so nothing records what exists. Running it
twice produces two clusters. There is no way to review a change before it lands,
no way to see what an edit would do, and no way for a second person to make a
change safely. The addons, IAM roles and VPC endpoints that a real cluster needs
are then applied by hand and exist only in whoever's shell history.

## Decision

All AWS infrastructure is Terraform, with remote state in S3 and locking in
DynamoDB.

## Alternatives considered

**eksctl.** Genuinely good for a throwaway cluster, and faster to first pod. It
has no answer for the second change, the second engineer, or the audit question
"what is actually deployed".

**CloudFormation / CDK.** Fine choices; CDK in particular is pleasant. Terraform
was chosen because it is the more common expectation for platform roles and
because its plan output is the clearest artefact to put in front of a reviewer.

**Crossplane.** Managing AWS from inside Kubernetes is elegant and removes a
tool. It also creates a bootstrapping problem — the cluster that manages the
infrastructure has to be created by something — and adds a controller to operate.
Not worth it at this size.

## Consequences

- `terraform plan` is a reviewable diff of an infrastructure change.
- State is shared and locked, so two people cannot apply at once.
- A cluster can be destroyed and recreated identically, which is what makes
  `make destroy` a safe habit rather than a frightening one.
- Cost: Terraform is slower than eksctl for a first cluster, and the state
  backend has to exist before the first apply.
