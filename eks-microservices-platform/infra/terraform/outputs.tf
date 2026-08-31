output "cluster_name" {
  description = "EKS cluster name."
  value       = aws_eks_cluster.this.name
}

output "cluster_endpoint" {
  description = "Kubernetes API server endpoint."
  value       = aws_eks_cluster.this.endpoint
}

output "cluster_version" {
  description = "Kubernetes version running on the control plane."
  value       = aws_eks_cluster.this.version
}

output "cluster_certificate_authority" {
  description = "Base64 CA bundle for the API server."
  value       = aws_eks_cluster.this.certificate_authority[0].data
  # Not a credential, but marking it sensitive keeps a 1KB blob out of every
  # plan and apply log.
  sensitive = true
}

output "oidc_provider_arn" {
  description = "IAM OIDC provider ARN, for IRSA trust policies."
  value       = aws_iam_openid_connect_provider.eks.arn
}

output "vpc_id" {
  description = "VPC ID."
  value       = aws_vpc.this.id
}

output "private_subnet_ids" {
  description = "Private subnet IDs where nodes and pods run."
  value       = aws_subnet.private[*].id
}

output "ecr_repository_urls" {
  description = "ECR repository URLs, keyed by service."
  value       = { for k, v in aws_ecr_repository.service : k => v.repository_url }
}

output "github_deploy_role_arn" {
  description = "Role for GitHub Actions to assume via OIDC. Set this as the AWS_DEPLOY_ROLE repository variable; no access key is needed."
  value       = aws_iam_role.github_deploy.arn
}

output "external_secrets_role_arn" {
  description = "IRSA role for the External Secrets controller."
  value       = aws_iam_role.external_secrets.arn
}

output "kubeconfig_command" {
  description = "Command to configure kubectl for this cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${aws_eks_cluster.this.name}"
}

output "estimated_monthly_cost_usd" {
  description = <<-EOT
    Rough fixed monthly cost, for awareness before applying. Excludes data
    transfer, EBS and load balancers.
  EOT
  value = {
    control_plane = 73
    nat_gateways  = local.nat_gateway_count * 32
    # Five interface endpoints at roughly $7 each per AZ.
    vpc_endpoints = length(aws_vpc_endpoint.interface) * 7 * var.availability_zone_count
    note          = "Nodes are billed separately and vary with spot pricing. See docs/cost.md and run 'make destroy' when finished."
  }
}
