# Development environment.
#
# Sized for cost, not availability. Roughly $105/month fixed before nodes:
# $73 control plane + $32 for one NAT gateway. See docs/cost.md.

environment = "dev"
region      = "us-east-1"

vpc_cidr                = "10.10.0.0/16"
availability_zone_count = 2

# One NAT gateway. It is a zone-level single point of failure for egress, which
# is the correct trade in dev and the wrong one in production.
single_nat_gateway = true

kubernetes_version = "1.31"

node_groups = {
  system = {
    instance_types = ["t3.small"]
    capacity_type  = "ON_DEMAND"
    min_size       = 1
    max_size       = 2
    desired_size   = 1
    disk_size      = 20
    labels         = { workload = "system" }
  }
  application = {
    instance_types = ["t3.medium", "t3a.medium"]
    capacity_type  = "SPOT"
    min_size       = 1
    max_size       = 4
    desired_size   = 2
    disk_size      = 30
    labels         = { workload = "application" }
  }
}

# Shorter retention: dev logs have no audit value and storage is billed per GB.
log_retention_days = 7
