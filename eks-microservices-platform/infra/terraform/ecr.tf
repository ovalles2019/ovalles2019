# Container registries, one per service.

resource "aws_ecr_repository" "service" {
  for_each = toset(["gateway", "catalog", "scorer"])

  name = "${local.name}/${each.value}"

  # IMMUTABLE is the important setting here. With mutable tags, `v1.2.3` can be
  # repointed at different content after it has been deployed and reviewed, so
  # the tag stops being evidence of what is running. Immutability is what makes
  # a rollback to a known tag mean anything.
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "KMS"
    kms_key         = aws_kms_key.eks.arn
  }

  tags = { Name = "${local.name}-${each.value}" }
}

# Untagged layers accumulate on every rebuild and are billed per GB forever.
# Expiring them and capping tagged history keeps registry cost flat instead of
# growing linearly with commit count.
resource "aws_ecr_lifecycle_policy" "service" {
  for_each = aws_ecr_repository.service

  repository = each.value.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 7 days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 7
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep the 30 most recent release images"
        selection = {
          tagStatus     = "tagged"
          tagPrefixList = ["v"]
          countType     = "imageCountMoreThan"
          countNumber   = 30
        }
        action = { type = "expire" }
      },
    ]
  })
}
