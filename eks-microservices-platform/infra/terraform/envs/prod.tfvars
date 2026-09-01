# Production environment.

environment = "prod"
region      = "us-east-1"

vpc_cidr                = "10.30.0.0/16"
availability_zone_count = 3

# One NAT gateway per AZ. Three times the cost, but a single shared NAT is a
# zone-level single point of failure for all outbound traffic — and every
# cross-zone byte through it is billed twice.
single_nat_gateway = false

kubernetes_version = "1.31"

node_groups = {
  system = {
    # On-demand for cluster-wide components. A spot reclaim that takes both
    # CoreDNS replicas breaks name resolution for every pod at once.
    instance_types = ["m5.large", "m5a.large"]
    capacity_type  = "ON_DEMAND"
    min_size       = 3
    max_size       = 6
    desired_size   = 3
    disk_size      = 50
    labels         = { workload = "system" }
    taints = [{
      # Reserve these nodes for system components. Without the taint, ordinary
      # workloads schedule here and the on-demand capacity bought for DNS and
      # controllers is consumed by whatever landed first.
      key    = "workload"
      value  = "system"
      effect = "NO_SCHEDULE"
    }]
  }
  application = {
    # Four instance types across two families. A spot pool restricted to one
    # type is far more likely to be simultaneously unavailable.
    instance_types = ["m5.xlarge", "m5a.xlarge", "m6i.xlarge", "m6a.xlarge"]
    capacity_type  = "SPOT"
    min_size       = 3
    max_size       = 20
    desired_size   = 4
    disk_size      = 100
    labels         = { workload = "application" }
  }
}

# Narrow this to the office and CI egress ranges. An internet-reachable API
# server endpoint is one leaked credential away from a cluster takeover.
cluster_endpoint_public_access       = true
cluster_endpoint_public_access_cidrs = ["0.0.0.0/0"]

# A year of audit logs. This is the record that answers "who deleted that" and
# it cannot be enabled retroactively.
log_retention_days = 365
