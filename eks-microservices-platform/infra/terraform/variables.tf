variable "project" {
  description = "Name prefix applied to every resource and used for cost allocation."
  type        = string
  default     = "fleet-platform"

  validation {
    # The value becomes part of resource names that must be DNS labels.
    condition     = can(regex("^[a-z][a-z0-9-]{2,24}$", var.project))
    error_message = "project must be 3-25 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "environment" {
  description = "Deployment environment. Drives sizing, availability and cost defaults."
  type        = string

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of: dev, staging, prod."
  }
}

variable "region" {
  description = "AWS region."
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  description = "CIDR for the VPC. Must be large enough for the pod IPs the VPC CNI allocates."
  type        = string
  default     = "10.0.0.0/16"

  validation {
    # The AWS VPC CNI gives every pod a real VPC IP, so subnet sizing is driven
    # by pod count, not node count. A /24 per subnet runs out of addresses long
    # before the nodes run out of capacity, and the symptom is pods stuck in
    # ContainerCreating with no obvious cause.
    condition     = can(cidrnetmask(var.vpc_cidr)) && tonumber(split("/", var.vpc_cidr)[1]) <= 20
    error_message = "vpc_cidr must be a valid CIDR of /20 or larger; the VPC CNI assigns a VPC IP per pod, so small VPCs exhaust addresses well before compute."
  }
}

variable "availability_zone_count" {
  description = "Number of AZs to spread subnets across."
  type        = number
  default     = 3

  validation {
    condition     = var.availability_zone_count >= 2 && var.availability_zone_count <= 4
    error_message = "availability_zone_count must be between 2 and 4; a single AZ cannot survive a zone failure."
  }
}

variable "kubernetes_version" {
  description = "EKS control plane version."
  type        = string
  default     = "1.31"
}

variable "single_nat_gateway" {
  description = <<-EOT
    Route all private egress through one NAT Gateway instead of one per AZ.

    A NAT Gateway is roughly $32/month plus data processing, so three of them is
    the single largest fixed cost in a small cluster. One is the right call for
    dev; in production it is a zone-level single point of failure for outbound
    traffic, so the prod tfvars sets this to false.
  EOT
  type        = bool
  default     = true
}

variable "node_groups" {
  description = "Managed node group definitions."
  type = map(object({
    instance_types = list(string)
    capacity_type  = string
    min_size       = number
    max_size       = number
    desired_size   = number
    disk_size      = number
    labels         = optional(map(string), {})
    taints = optional(list(object({
      key    = string
      value  = string
      effect = string
    })), [])
  }))

  default = {
    system = {
      # On-demand for the components whose eviction is disruptive: CoreDNS,
      # the metrics server, controllers. Running these on spot means a spot
      # reclaim can take out cluster-wide services.
      instance_types = ["t3.medium"]
      capacity_type  = "ON_DEMAND"
      min_size       = 2
      max_size       = 4
      desired_size   = 2
      disk_size      = 30
      labels         = { workload = "system" }
    }
    application = {
      # Several instance types, because a spot pool with one type is far more
      # likely to have no capacity. Spot is ~70% cheaper and appropriate here:
      # every service has a PDB and multi-replica topology spreading, so an
      # interruption costs a rescheduling, not availability.
      instance_types = ["t3.large", "t3a.large", "m5.large", "m5a.large"]
      capacity_type  = "SPOT"
      min_size       = 2
      max_size       = 10
      desired_size   = 3
      disk_size      = 50
      labels         = { workload = "application" }
    }
  }
}

variable "cluster_endpoint_public_access" {
  description = "Whether the API server endpoint is reachable from the internet."
  type        = bool
  default     = true
}

variable "cluster_endpoint_public_access_cidrs" {
  description = <<-EOT
    CIDRs allowed to reach the public API server endpoint.

    The default is deliberately 0.0.0.0/0 only so a first `terraform apply` from
    an unknown address works. Narrow it to the office and CI egress ranges
    before this environment holds anything real; an internet-reachable API
    server is one credential leak away from a cluster takeover.
  EOT
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "log_retention_days" {
  description = "CloudWatch retention for control plane logs. Unset means keep forever, which is a quietly growing bill."
  type        = number
  default     = 30
}

variable "enabled_cluster_log_types" {
  description = "Control plane log types to publish."
  type        = list(string)
  # The audit log is what answers "who deleted that" after an incident. It
  # cannot be enabled retroactively, so it is on from the start.
  default = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
}

variable "github_repository" {
  description = "owner/repo allowed to assume the deploy role via GitHub OIDC."
  type        = string
  default     = "ovalles2019/eks-microservices-platform"

  validation {
    condition     = can(regex("^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$", var.github_repository))
    error_message = "github_repository must be in owner/repo form."
  }
}

variable "create_github_oidc_provider" {
  description = "Create the GitHub OIDC provider. Set false when the account already has one; it is account-wide and can only exist once."
  type        = bool
  default     = true
}
