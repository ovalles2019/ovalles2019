# EKS control plane, node groups and addons.

# --- Encryption --------------------------------------------------------------

resource "aws_kms_key" "eks" {
  description = "Envelope encryption for Kubernetes Secrets in ${local.name}."
  # Rotation is free and removes the need to ever plan a manual key rotation.
  enable_key_rotation     = true
  deletion_window_in_days = 30
}

resource "aws_kms_alias" "eks" {
  name          = "alias/${local.name}-eks"
  target_key_id = aws_kms_key.eks.key_id
}

resource "aws_kms_key" "logs" {
  description             = "Encryption for ${local.name} CloudWatch log groups."
  enable_key_rotation     = true
  deletion_window_in_days = 30
  policy                  = data.aws_iam_policy_document.logs_kms.json
}

data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

data "aws_iam_policy_document" "logs_kms" {
  statement {
    sid       = "EnableIAMUserPermissions"
    effect    = "Allow"
    actions   = ["kms:*"]
    resources = ["*"]

    principals {
      type        = "AWS"
      identifiers = ["arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
  }

  statement {
    sid    = "AllowCloudWatchLogs"
    effect = "Allow"
    actions = [
      "kms:Encrypt*",
      "kms:Decrypt*",
      "kms:ReEncrypt*",
      "kms:GenerateDataKey*",
      "kms:Describe*",
    ]
    resources = ["*"]

    principals {
      type        = "Service"
      identifiers = ["logs.${var.region}.amazonaws.com"]
    }

    # Without this condition the key is usable by the CloudWatch Logs service
    # on behalf of any account, not just this one.
    condition {
      test     = "ArnLike"
      variable = "kms:EncryptionContext:aws:logs:arn"
      values   = ["arn:${data.aws_partition.current.partition}:logs:${var.region}:${data.aws_caller_identity.current.account_id}:log-group:*"]
    }
  }
}

# --- Control plane -----------------------------------------------------------

resource "aws_cloudwatch_log_group" "cluster" {
  # EKS writes to this exact name. Creating it here rather than letting EKS do
  # it is what allows a retention period and a KMS key to be set; the group EKS
  # creates implicitly retains logs forever.
  name              = "/aws/eks/${local.name}/cluster"
  retention_in_days = var.log_retention_days
  kms_key_id        = aws_kms_key.logs.arn
}

resource "aws_eks_cluster" "this" {
  name     = local.name
  version  = var.kubernetes_version
  role_arn = aws_iam_role.cluster.arn

  enabled_cluster_log_types = var.enabled_cluster_log_types

  vpc_config {
    # Nodes live only in private subnets. Public subnets carry load balancers
    # and NAT gateways and nothing else.
    subnet_ids              = aws_subnet.private[*].id
    endpoint_private_access = true
    endpoint_public_access  = var.cluster_endpoint_public_access
    public_access_cidrs     = var.cluster_endpoint_public_access_cidrs
    security_group_ids      = [aws_security_group.cluster.id]
  }

  # Envelope encryption for Secrets. Without it, Secret values sit in etcd
  # encoded but not encrypted, so anyone with an etcd snapshot or a control
  # plane backup has every credential in the cluster.
  encryption_config {
    provider {
      key_arn = aws_kms_key.eks.arn
    }
    resources = ["secrets"]
  }

  access_config {
    # API access entries rather than the aws-auth ConfigMap. aws-auth is a
    # single cluster-wide ConfigMap with no validation: one bad edit locks
    # everyone out of the cluster with no way back in short of recreating it.
    authentication_mode                         = "API_AND_CONFIG_MAP"
    bootstrap_cluster_creator_admin_permissions = true
  }

  depends_on = [
    aws_iam_role_policy_attachment.cluster_policy,
    aws_cloudwatch_log_group.cluster,
  ]
}

resource "aws_security_group" "cluster" {
  name        = "${local.name}-cluster"
  description = "EKS control plane security group."
  vpc_id      = aws_vpc.this.id

  egress {
    description = "Control plane egress"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${local.name}-cluster" }
}

# --- IRSA --------------------------------------------------------------------

data "tls_certificate" "oidc" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

# The OIDC provider is what makes IRSA work: a pod's ServiceAccount token is
# exchanged for short-lived AWS credentials scoped to one role. The alternative
# is granting permissions to the node instance profile, which gives every pod on
# the node the union of everything any pod on it needs.
resource "aws_iam_openid_connect_provider" "eks" {
  url             = aws_eks_cluster.this.identity[0].oidc[0].issuer
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.oidc.certificates[0].sha1_fingerprint]
}

# --- Node groups -------------------------------------------------------------

resource "aws_launch_template" "node" {
  for_each = var.node_groups

  name_prefix = "${local.name}-${each.key}-"

  # IMDSv2 required. IMDSv1 answers an unauthenticated GET, so any
  # server-side request forgery in a pod can read the node's instance
  # credentials; requiring a token closes that path.
  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required"
    # A hop limit of 1 stops a container from reaching IMDS at all, since the
    # request would need a second hop out of the pod network namespace.
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "enabled"
  }

  block_device_mappings {
    device_name = "/dev/xvda"

    ebs {
      volume_size = each.value.disk_size
      volume_type = "gp3"
      encrypted   = true
      kms_key_id  = aws_kms_key.eks.arn
      # gp3 includes 3000 IOPS at no extra cost; leaving it at the default
      # gives up free performance.
      iops                  = 3000
      throughput            = 125
      delete_on_termination = true
    }
  }

  monitoring {
    enabled = true
  }

  tag_specifications {
    resource_type = "instance"
    tags = {
      Name = "${local.name}-${each.key}"
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_eks_node_group" "this" {
  for_each = var.node_groups

  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "${local.name}-${each.key}"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = aws_subnet.private[*].id

  instance_types = each.value.instance_types
  capacity_type  = each.value.capacity_type

  scaling_config {
    min_size     = each.value.min_size
    max_size     = each.value.max_size
    desired_size = each.value.desired_size
  }

  update_config {
    # One node at a time. Higher values drain several nodes at once, which a
    # PodDisruptionBudget then blocks, and the update stalls partway through.
    max_unavailable = 1
  }

  launch_template {
    id      = aws_launch_template.node[each.key].id
    version = aws_launch_template.node[each.key].latest_version
  }

  labels = each.value.labels

  dynamic "taint" {
    for_each = each.value.taints
    content {
      key    = taint.value.key
      value  = taint.value.value
      effect = taint.value.effect
    }
  }

  lifecycle {
    # desired_size is handed to the cluster autoscaler or Karpenter after
    # creation. Leaving it under Terraform's control means every apply resets
    # the cluster to its baseline size, undoing whatever scaling just happened.
    ignore_changes = [scaling_config[0].desired_size]
  }

  depends_on = [
    aws_iam_role_policy_attachment.node_worker,
    aws_iam_role_policy_attachment.node_cni,
    aws_iam_role_policy_attachment.node_ecr,
  ]

  tags = { Name = "${local.name}-${each.key}" }
}

# --- Addons ------------------------------------------------------------------

# Addon versions are resolved by a data source rather than hardcoded, so a
# cluster upgrade picks up the matching addon version instead of failing on an
# incompatible one pinned months earlier.
data "aws_eks_addon_version" "this" {
  for_each = toset(["vpc-cni", "coredns", "kube-proxy", "aws-ebs-csi-driver", "eks-pod-identity-agent"])

  addon_name         = each.value
  kubernetes_version = aws_eks_cluster.this.version
  most_recent        = true
}

resource "aws_eks_addon" "vpc_cni" {
  cluster_name  = aws_eks_cluster.this.name
  addon_name    = "vpc-cni"
  addon_version = data.aws_eks_addon_version.this["vpc-cni"].version

  # PRESERVE keeps any in-cluster changes on conflict rather than silently
  # reverting them mid-upgrade.
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"

  service_account_role_arn = aws_iam_role.vpc_cni.arn

  configuration_values = jsonencode({
    env = {
      # Prefix delegation multiplies the pod IPs available per node by
      # assigning /28 blocks instead of individual addresses. Without it, node
      # pod density is capped by ENI limits — a t3.medium tops out at 17 pods
      # regardless of how much CPU and memory are free.
      ENABLE_PREFIX_DELEGATION = "true"
      WARM_PREFIX_TARGET       = "1"
    }
  })
}

resource "aws_eks_addon" "coredns" {
  cluster_name  = aws_eks_cluster.this.name
  addon_name    = "coredns"
  addon_version = data.aws_eks_addon_version.this["coredns"].version

  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"

  # CoreDNS must land on the on-demand system nodes: DNS is a cluster-wide
  # dependency, and a spot reclaim that takes both replicas breaks resolution
  # for every pod at once.
  configuration_values = jsonencode({
    nodeSelector = { workload = "system" }
    replicaCount = 2
  })

  depends_on = [aws_eks_node_group.this]
}

resource "aws_eks_addon" "kube_proxy" {
  cluster_name  = aws_eks_cluster.this.name
  addon_name    = "kube-proxy"
  addon_version = data.aws_eks_addon_version.this["kube-proxy"].version

  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"
}

resource "aws_eks_addon" "ebs_csi" {
  cluster_name  = aws_eks_cluster.this.name
  addon_name    = "aws-ebs-csi-driver"
  addon_version = data.aws_eks_addon_version.this["aws-ebs-csi-driver"].version

  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"

  service_account_role_arn = aws_iam_role.ebs_csi.arn

  depends_on = [aws_eks_node_group.this]
}

# Pod Identity is the successor to IRSA: it drops the OIDC trust-policy
# plumbing and scopes a role to a namespace and ServiceAccount directly. Both
# are enabled here because most controllers still document IRSA.
resource "aws_eks_addon" "pod_identity" {
  cluster_name  = aws_eks_cluster.this.name
  addon_name    = "eks-pod-identity-agent"
  addon_version = data.aws_eks_addon_version.this["eks-pod-identity-agent"].version

  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"

  depends_on = [aws_eks_node_group.this]
}
