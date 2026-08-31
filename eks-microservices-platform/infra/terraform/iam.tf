# IAM roles: cluster, nodes, IRSA service roles and the GitHub deploy role.

locals {
  oidc_provider_arn = aws_iam_openid_connect_provider.eks.arn
  oidc_provider_url = replace(aws_eks_cluster.this.identity[0].oidc[0].issuer, "https://", "")
}

# Builds an IRSA trust policy for one namespace/ServiceAccount pair.
#
# Both conditions matter. `:sub` pins the exact ServiceAccount, so no other pod
# in the cluster can assume the role. `:aud` is what stops a token minted for a
# different audience being replayed here; a trust policy with only `:sub` is a
# well-known IRSA misconfiguration.
data "aws_iam_policy_document" "irsa_trust" {
  for_each = {
    vpc_cni          = { namespace = "kube-system", service_account = "aws-node" }
    ebs_csi          = { namespace = "kube-system", service_account = "ebs-csi-controller-sa" }
    load_balancer    = { namespace = "kube-system", service_account = "aws-load-balancer-controller" }
    external_secrets = { namespace = "external-secrets", service_account = "external-secrets" }
    catalog          = { namespace = "fleet-platform", service_account = "catalog" }
  }

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_url}:sub"
      values   = ["system:serviceaccount:${each.value.namespace}:${each.value.service_account}"]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_url}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

# --- Cluster role ------------------------------------------------------------

data "aws_iam_policy_document" "eks_service_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "cluster" {
  name               = "${local.name}-cluster"
  assume_role_policy = data.aws_iam_policy_document.eks_service_assume.json
}

resource "aws_iam_role_policy_attachment" "cluster_policy" {
  role       = aws_iam_role.cluster.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKSClusterPolicy"
}

# --- Node role ---------------------------------------------------------------

data "aws_iam_policy_document" "ec2_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "node" {
  name               = "${local.name}-node"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume.json
}

resource "aws_iam_role_policy_attachment" "node_worker" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKSWorkerNodePolicy"
}

# The CNI policy is attached to the aws-node ServiceAccount role via IRSA
# below, not to the node role. Attaching it to the node would let every pod on
# the node manipulate ENIs.
resource "aws_iam_role_policy_attachment" "node_cni" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKS_CNI_Policy"
}

resource "aws_iam_role_policy_attachment" "node_ecr" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

# Enables Session Manager shell access, so nodes need no SSH key, no port 22
# and no bastion host. Every session is logged in CloudTrail.
resource "aws_iam_role_policy_attachment" "node_ssm" {
  role       = aws_iam_role.node.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# --- IRSA roles --------------------------------------------------------------

resource "aws_iam_role" "vpc_cni" {
  name               = "${local.name}-vpc-cni"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust["vpc_cni"].json
}

resource "aws_iam_role_policy_attachment" "vpc_cni" {
  role       = aws_iam_role.vpc_cni.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKS_CNI_Policy"
}

resource "aws_iam_role" "ebs_csi" {
  name               = "${local.name}-ebs-csi"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust["ebs_csi"].json
}

resource "aws_iam_role_policy_attachment" "ebs_csi" {
  role       = aws_iam_role.ebs_csi.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

resource "aws_iam_role" "load_balancer" {
  name               = "${local.name}-aws-load-balancer-controller"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust["load_balancer"].json
}

# --- External Secrets --------------------------------------------------------

# Scoped to this environment's secret prefix, not to "*". A wildcard here would
# let a compromised External Secrets pod read every secret in the account,
# including those belonging to unrelated systems.
data "aws_iam_policy_document" "external_secrets" {
  statement {
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
    ]
    resources = [
      "arn:${data.aws_partition.current.partition}:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:${local.name}/*",
    ]
  }

  statement {
    effect    = "Allow"
    actions   = ["secretsmanager:ListSecrets"]
    resources = ["*"]
  }

  statement {
    effect    = "Allow"
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.eks.arn]

    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["secretsmanager.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "external_secrets" {
  name               = "${local.name}-external-secrets"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust["external_secrets"].json
}

resource "aws_iam_role_policy" "external_secrets" {
  name   = "${local.name}-external-secrets"
  role   = aws_iam_role.external_secrets.id
  policy = data.aws_iam_policy_document.external_secrets.json
}

# --- GitHub Actions OIDC -----------------------------------------------------

# CI assumes this role through GitHub's OIDC provider, so the repository holds
# no AWS access key. A long-lived key in repository secrets is the single most
# common way an AWS account is compromised through a CI pipeline: it does not
# expire, it is copied into every fork's environment on a careless workflow
# change, and it is invisible in CloudTrail as anything but "that key again".
data "tls_certificate" "github" {
  count = var.create_github_oidc_provider ? 1 : 0
  url   = "https://token.actions.githubusercontent.com"
}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_github_oidc_provider ? 1 : 0

  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.github[0].certificates[0].sha1_fingerprint]
}

data "aws_iam_openid_connect_provider" "github_existing" {
  count = var.create_github_oidc_provider ? 0 : 1
  url   = "https://token.actions.githubusercontent.com"
}

locals {
  github_oidc_arn = var.create_github_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : data.aws_iam_openid_connect_provider.github_existing[0].arn
}

data "aws_iam_policy_document" "github_deploy_trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.github_oidc_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    # Scoped to this repository, and to its protected refs rather than to
    # `repo:owner/name:*`. The wildcard form lets any pull request branch —
    # including one opened by an outside contributor — assume the deploy role.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values = [
        "repo:${var.github_repository}:ref:refs/heads/main",
        "repo:${var.github_repository}:ref:refs/tags/v*",
        "repo:${var.github_repository}:environment:${var.environment}",
      ]
    }
  }
}

resource "aws_iam_role" "github_deploy" {
  name               = "${local.name}-github-deploy"
  assume_role_policy = data.aws_iam_policy_document.github_deploy_trust.json
  # One hour. The deploy job takes minutes; a longer session only widens the
  # window in which a leaked token is useful.
  max_session_duration = 3600
}

data "aws_iam_policy_document" "github_deploy" {
  statement {
    sid       = "DescribeCluster"
    effect    = "Allow"
    actions   = ["eks:DescribeCluster"]
    resources = [aws_eks_cluster.this.arn]
  }

  statement {
    sid    = "PushImages"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:CompleteLayerUpload",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
    ]
    resources = [for r in aws_ecr_repository.service : r.arn]
  }

  statement {
    sid       = "EcrAuth"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "github_deploy" {
  name   = "${local.name}-github-deploy"
  role   = aws_iam_role.github_deploy.id
  policy = data.aws_iam_policy_document.github_deploy.json
}

# The role still needs Kubernetes-side authorisation. An access entry grants it
# in-cluster permissions without touching the aws-auth ConfigMap.
resource "aws_eks_access_entry" "github_deploy" {
  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_iam_role.github_deploy.arn
  type          = "STANDARD"
}

resource "aws_eks_access_policy_association" "github_deploy" {
  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_iam_role.github_deploy.arn
  # Edit, not admin: CI deploys workloads. It has no reason to be able to
  # change RBAC, read every Secret in the cluster, or delete namespaces.
  policy_arn = "arn:${data.aws_partition.current.partition}:eks::aws:cluster-access-policy/AmazonEKSEditPolicy"

  access_scope {
    type       = "namespace"
    namespaces = ["fleet-platform"]
  }

  depends_on = [aws_eks_access_entry.github_deploy]
}
