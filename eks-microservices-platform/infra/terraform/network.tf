# VPC, subnets, NAT and VPC endpoints.

data "aws_availability_zones" "available" {
  state = "available"

  filter {
    # Local Zones and Wavelength zones appear here but do not run EKS node
    # groups; selecting one produces a confusing capacity failure at apply time.
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  name = "${var.project}-${var.environment}"

  azs = slice(data.aws_availability_zones.available.names, 0, var.availability_zone_count)

  # /20 per subnet — 4,091 usable addresses. Sized for pods, not nodes: the AWS
  # VPC CNI assigns every pod a real VPC IP, so a /24 here would cap the cluster
  # at a few hundred pods regardless of how much compute is available.
  private_subnets = [for i, az in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i, az in local.azs : cidrsubnet(var.vpc_cidr, 8, i + 128)]

  # NAT gateways are the largest fixed cost in a small cluster. One in dev, one
  # per AZ in production so a zone failure cannot sever outbound traffic for the
  # whole cluster.
  nat_gateway_count = var.single_nat_gateway ? 1 : length(local.azs)
}

resource "aws_vpc" "this" {
  cidr_block         = var.vpc_cidr
  enable_dns_support = true
  # Required by the VPC CNI and by any private endpoint that relies on private
  # DNS. Without it, in-cluster resolution of AWS service names silently fails.
  enable_dns_hostnames = true

  tags = { Name = local.name }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = local.name }
}

resource "aws_subnet" "private" {
  count = length(local.private_subnets)

  vpc_id            = aws_vpc.this.id
  cidr_block        = local.private_subnets[count.index]
  availability_zone = local.azs[count.index]

  tags = {
    Name = "${local.name}-private-${local.azs[count.index]}"
    # The AWS Load Balancer Controller discovers subnets by these tags. Without
    # them an internal Ingress fails to provision with an error that does not
    # mention subnets at all.
    "kubernetes.io/role/internal-elb"     = "1"
    "kubernetes.io/cluster/${local.name}" = "shared"
    "karpenter.sh/discovery"              = local.name
  }
}

resource "aws_subnet" "public" {
  count = length(local.public_subnets)

  vpc_id            = aws_vpc.this.id
  cidr_block        = local.public_subnets[count.index]
  availability_zone = local.azs[count.index]

  # Nodes never live here, so no public IP is auto-assigned. Only load
  # balancers and NAT gateways occupy the public subnets.
  map_public_ip_on_launch = false

  tags = {
    Name                                  = "${local.name}-public-${local.azs[count.index]}"
    "kubernetes.io/role/elb"              = "1"
    "kubernetes.io/cluster/${local.name}" = "shared"
  }
}

resource "aws_eip" "nat" {
  count  = local.nat_gateway_count
  domain = "vpc"
  tags   = { Name = "${local.name}-nat-${count.index}" }

  depends_on = [aws_internet_gateway.this]
}

resource "aws_nat_gateway" "this" {
  count = local.nat_gateway_count

  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id
  tags          = { Name = "${local.name}-nat-${count.index}" }

  depends_on = [aws_internet_gateway.this]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = "${local.name}-public" }
}

resource "aws_route" "public_internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.this.id
}

resource "aws_route_table_association" "public" {
  count = length(aws_subnet.public)

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# One route table per private subnet, so each can point at its own zone's NAT
# gateway when they are per-AZ. Sharing one table would send all egress through
# a single zone and add a cross-AZ data charge to every outbound byte.
resource "aws_route_table" "private" {
  count = length(aws_subnet.private)

  vpc_id = aws_vpc.this.id
  tags   = { Name = "${local.name}-private-${local.azs[count.index]}" }
}

resource "aws_route" "private_nat" {
  count = length(aws_subnet.private)

  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.this[var.single_nat_gateway ? 0 : count.index].id
}

resource "aws_route_table_association" "private" {
  count = length(aws_subnet.private)

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# --- VPC endpoints -----------------------------------------------------------

# Gateway endpoints for S3 and DynamoDB are free and route that traffic off the
# NAT gateway entirely. Every ECR image pull downloads its layers from S3, so
# without this endpoint image pulls are billed as NAT data processing — usually
# a surprising fraction of a cluster's networking bill.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id

  tags = { Name = "${local.name}-s3" }
}

resource "aws_security_group" "vpc_endpoints" {
  name        = "${local.name}-vpc-endpoints"
  description = "Allow HTTPS from within the VPC to interface endpoints."
  vpc_id      = aws_vpc.this.id

  ingress {
    description = "HTTPS from inside the VPC"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = { Name = "${local.name}-vpc-endpoints" }
}

# Interface endpoints are not free (roughly $7/month each plus data), so only
# the ones every node uses constantly are created. In production they also keep
# control-plane-adjacent traffic off the public internet entirely.
resource "aws_vpc_endpoint" "interface" {
  for_each = toset([
    "ecr.api",
    "ecr.dkr",
    "sts",
    "logs",
    "secretsmanager",
  ])

  vpc_id              = aws_vpc.this.id
  service_name        = "com.amazonaws.${var.region}.${each.value}"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = aws_subnet.private[*].id
  security_group_ids  = [aws_security_group.vpc_endpoints.id]
  private_dns_enabled = true

  tags = { Name = "${local.name}-${each.value}" }
}

# --- Flow logs ---------------------------------------------------------------

resource "aws_cloudwatch_log_group" "flow_logs" {
  name              = "/aws/vpc/${local.name}/flow-logs"
  retention_in_days = var.log_retention_days
  kms_key_id        = aws_kms_key.logs.arn
}

resource "aws_flow_log" "this" {
  # REJECT only, not ALL. Accepted-traffic logs at cluster scale are enormous
  # and mostly noise; rejects are what actually answer "why can this pod not
  # reach that endpoint", which is the question a NetworkPolicy rollout raises.
  traffic_type         = "REJECT"
  vpc_id               = aws_vpc.this.id
  iam_role_arn         = aws_iam_role.flow_logs.arn
  log_destination      = aws_cloudwatch_log_group.flow_logs.arn
  log_destination_type = "cloud-watch-logs"

  tags = { Name = "${local.name}-flow-logs" }
}

resource "aws_iam_role" "flow_logs" {
  name               = "${local.name}-flow-logs"
  assume_role_policy = data.aws_iam_policy_document.flow_logs_assume.json
}

data "aws_iam_policy_document" "flow_logs_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["vpc-flow-logs.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "flow_logs" {
  statement {
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
      "logs:DescribeLogGroups",
      "logs:DescribeLogStreams",
    ]
    resources = ["${aws_cloudwatch_log_group.flow_logs.arn}:*"]
  }
}

resource "aws_iam_role_policy" "flow_logs" {
  name   = "${local.name}-flow-logs"
  role   = aws_iam_role.flow_logs.id
  policy = data.aws_iam_policy_document.flow_logs.json
}
