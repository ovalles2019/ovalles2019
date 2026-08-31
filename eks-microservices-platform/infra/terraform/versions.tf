terraform {
  # Pinned to a minor version rather than left open. A provider that changes
  # a default between runs produces a plan nobody asked for, and "terraform
  # apply rewrote resources I did not touch" is how teams stop trusting IaC.
  required_version = "~> 1.9"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.70"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # State is remote and locked. The reference this project improves on created
  # its cluster with an `eksctl` command pasted from a README, which leaves no
  # state at all: nothing records what exists, two people running it produce two
  # clusters, and there is no way to review a change before it lands.
  #
  # Values come from -backend-config so the same code serves every environment.
  backend "s3" {
    # bucket         = "fleet-platform-tfstate-<account-id>"
    # key            = "eks/<environment>/terraform.tfstate"
    # region         = "us-east-1"
    # dynamodb_table = "fleet-platform-tflock"
    # encrypt        = true
  }
}

provider "aws" {
  region = var.region

  default_tags {
    # Applied to every taggable resource, which is what makes cost allocation
    # and orphan cleanup possible later. Retrofitting tags across an account is
    # far more work than setting them once here.
    tags = {
      Project     = var.project
      Environment = var.environment
      ManagedBy   = "terraform"
      Repository  = "eks-microservices-platform"
    }
  }
}
