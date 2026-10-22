# Sketch of the AWS platform for orderflow: VPC, EKS and one RDS Postgres
# (Multi-AZ) with a database per service. Built on community modules.
terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.70" }
  }
}

locals {
  name = "orderflow-${var.environment}"
  tags = merge(var.tags, { Project = "orderflow", Environment = var.environment, ManagedBy = "terraform" })
}

data "aws_availability_zones" "available" { state = "available" }

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.13"

  name                = local.name
  cidr                = var.vpc_cidr
  azs                 = slice(data.aws_availability_zones.available.names, 0, 3)
  private_subnets     = [for i in range(3) : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets      = [for i in range(3) : cidrsubnet(var.vpc_cidr, 8, 48 + i)]
  database_subnets    = [for i in range(3) : cidrsubnet(var.vpc_cidr, 8, 64 + i)]
  enable_nat_gateway  = true
  single_nat_gateway  = var.environment != "prod"
  private_subnet_tags = { "kubernetes.io/role/internal-elb" = 1 }
  public_subnet_tags  = { "kubernetes.io/role/elb" = 1 }
  tags                = local.tags
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.24"

  cluster_name                   = local.name
  cluster_version                = var.kubernetes_version
  vpc_id                         = module.vpc.vpc_id
  subnet_ids                     = module.vpc.private_subnets
  cluster_endpoint_public_access = var.environment != "prod"
  enable_irsa                    = true

  eks_managed_node_groups = {
    general = {
      instance_types = var.node_instance_types
      min_size       = var.node_min
      max_size       = var.node_max
      desired_size   = var.node_min
      capacity_type  = var.environment == "prod" ? "ON_DEMAND" : "SPOT"
    }
  }
  tags = local.tags
}

resource "aws_security_group" "db" {
  name   = "${local.name}-db"
  vpc_id = module.vpc.vpc_id
  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks.node_security_group_id]
  }
  tags = local.tags
}

module "rds" {
  source  = "terraform-aws-modules/rds/aws"
  version = "~> 6.9"

  identifier                   = local.name
  engine                       = "postgres"
  engine_version               = "16"
  family                       = "postgres16"
  major_engine_version         = "16"
  instance_class               = var.db_instance_class
  allocated_storage            = 50
  max_allocated_storage        = 500
  storage_encrypted            = true
  multi_az                     = var.environment == "prod"
  db_name                      = "orderflow"
  username                     = "orderflow"
  manage_master_user_password  = true # stored in Secrets Manager, never in state outputs
  db_subnet_group_name         = module.vpc.database_subnet_group_name
  vpc_security_group_ids       = [aws_security_group.db.id]
  backup_retention_period      = var.environment == "prod" ? 14 : 3
  deletion_protection          = var.environment == "prod"
  performance_insights_enabled = true
  parameters                   = [{ name = "rds.force_ssl", value = "1" }]
  tags                         = local.tags
}
