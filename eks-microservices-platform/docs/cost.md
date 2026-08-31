# Cost

A portfolio project that quietly bills $300/month is not a good portfolio
project. This is what it costs, why, and how to make it stop.

## Fixed monthly cost, before any compute

| Component | dev | prod | Note |
|---|---|---|---|
| EKS control plane | $73 | $73 | Per cluster. Unavoidable, not prorated by size. |
| NAT Gateway | $32 | $97 | 1 in dev, 3 in prod (one per AZ). |
| Interface VPC endpoints | $14 | $105 | 5 endpoints x ~$7 x AZ count. |
| CloudWatch logs | ~$3 | ~$25 | Driven by audit log volume. |
| **Subtotal** | **~$122** | **~$300** | |

Compute is on top: roughly $30-60/month in dev on spot, and $200-400 in prod
depending on load and spot pricing.

## Where the money actually goes

**The control plane is the floor.** $73/month regardless of whether the cluster
runs anything. There is no smaller EKS.

**NAT Gateways are the biggest lever.** $32/month each plus $0.045/GB processed.
Three of them is nearly $100 before a byte moves. Dev uses one and accepts that
it is a zone-level single point of failure for egress; production uses three
because it is not.

**The S3 gateway endpoint is free and saves real money.** Every ECR image pull
downloads its layers from S3. Without the endpoint that traffic is billed as NAT
data processing — frequently a surprising fraction of a cluster's networking
bill. The endpoint costs nothing.

**Interface endpoints are not free** (~$7/month each, per AZ) and are the one
place this project spends money for security rather than saving it. Five
endpoints across three AZs is $105/month in production. In dev, dropping to two
AZs halves it. They can be removed entirely if the traffic is acceptable over
NAT — the trade is cost against keeping that traffic off the public internet.

## Deliberate savings

- **Spot for application nodes** (`ADR 0007`): ~70% off, safe here because every
  service has a PDB, topology spread and graceful shutdown.
- **gp3 rather than gp2**: ~20% cheaper per GB and includes 3000 IOPS free.
- **ECR lifecycle policies**: untagged layers expire after 7 days, tagged
  releases cap at 30. Without them registry storage grows linearly with commit
  count forever.
- **Log retention set explicitly**: 7 days in dev, 365 in prod. An unset
  retention means "keep forever", which is a quietly growing bill nobody
  attributes to anything.
- **Flow logs are REJECT-only**: accepted-traffic logs at cluster scale are
  enormous and mostly noise.
- **One Ingress, not a LoadBalancer per service**: a LoadBalancer Service
  provisions an ELB each. The conftest policy rejects them.

## Running this for free

The entire Kubernetes layer runs on kind at zero cost:

```bash
make kind-up      # 3-worker cluster with zone labels, Calico, metrics-server
make load-test    # drive the HPA
make kind-down
```

Only the Terraform layer needs AWS. That is the deliberate design: nobody should
have to pay to evaluate this.

## If you do apply the Terraform

```bash
make tf-plan ENV=dev          # review first; the plan shows every resource
terraform -chdir=infra/terraform apply -var-file=envs/dev.tfvars

# and when you are done:
terraform -chdir=infra/terraform destroy -var-file=envs/dev.tfvars
```

`terraform destroy` removes everything it created. Two things it will not clean
up, because Terraform did not create them: the S3 state bucket and DynamoDB lock
table, and any load balancers created by the AWS Load Balancer Controller in
response to an Ingress. Delete Ingress resources before destroying, or the
controller's ELBs are orphaned and keep billing.

Set a billing alarm before the first apply, not after.
