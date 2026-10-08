locals {
  name           = "statusboard-eks"
  cluster_name   = "statusboard-eks"
  alb_group_name = "statusboard-platform" # one shared ALB for staging, prod and Grafana
}

# ---- Lookups (created outside this stack) ----
data "aws_route53_zone" "zone" {
  name         = var.domain_name
  private_zone = false
}

# Wildcard certificate issued by the bootstrap stack
data "aws_acm_certificate" "cert" {
  domain      = var.domain_name
  statuses    = ["ISSUED"]
  most_recent = true
}

# GitHub Actions role created by the bootstrap stack
data "aws_iam_role" "github_actions" {
  name = "${local.name}-github-actions"
}

# ---- Network ----
module "vpc" {
  source             = "./module/vpc"
  name               = local.name
  cluster_name       = local.cluster_name
  nat_gateway_per_az = var.nat_gateway_per_az
}

# ---- EKS cluster ----
module "eks" {
  source             = "./module/eks"
  cluster_name       = local.cluster_name
  kubernetes_version = var.kubernetes_version
  instance_types     = var.node_instance_types
  private_subnet_ids = [
    module.vpc.private_subnet_ids["pri1"],
    module.vpc.private_subnet_ids["pri2"],
    module.vpc.private_subnet_ids["pri3"]
  ]
  admin_principal_arns = distinct(concat([data.aws_iam_role.github_actions.arn], var.admin_principal_arns))
}

# ---- Container registry ----
module "ecr" {
  source       = "./module/ecr"
  repositories = ["${local.name}/statusboard"]
}

# ---- Load balancer controller, DNS, metrics, storage ----
module "eks_addons" {
  source         = "./module/eks-addons"
  cluster_name   = module.eks.cluster_name
  region         = var.region
  vpc_id         = module.vpc.vpc_id
  domain_name    = var.domain_name
  hosted_zone_id = data.aws_route53_zone.zone.zone_id
}

# ---- Prometheus and Grafana ----
module "monitoring" {
  source                 = "./module/monitoring"
  domain_name            = var.domain_name
  certificate_arn        = data.aws_acm_certificate.cert.arn
  alb_group_name         = local.alb_group_name
  storage_class_name     = module.eks_addons.storage_class_name
  grafana_admin_password = var.grafana_admin_password
  expose_prometheus      = var.expose_prometheus

  # The ALB controller webhook must be running before any Ingress is created,
  # and on destroy the Ingresses (ALBs) are removed before the controller.
  depends_on = [module.eks_addons]
}

# ---- PostgreSQL operator: highly available databases (3-pod StatefulSets) ----
module "postgres_operator" {
  source = "./module/postgres-operator"

  # needs the gp3 StorageClass and running nodes
  depends_on = [module.eks_addons]
}

# ---- Staging and prod: namespaces, backups, monthly data exports ----
module "app_environments" {
  source       = "./module/app-environments"
  name         = local.name
  cluster_name = module.eks.cluster_name
  environments = var.environments

  data_team_user_names = var.data_team_user_names

  # Namespaces need the cluster; Pod Identity needs its agent add-on
  depends_on = [module.eks]
}
