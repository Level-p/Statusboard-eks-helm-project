output "cluster_name" {
  value = module.eks.cluster_name
}

output "region" {
  value = var.region
}

output "configure_kubectl" {
  description = "Run this to point kubectl at the cluster"
  value       = "aws eks update-kubeconfig --name ${module.eks.cluster_name} --region ${var.region}"
}

output "ecr_repository_url" {
  description = "Used by the GitHub Actions app workflow"
  value       = module.ecr.repository_urls["${local.name}/statusboard"]
}

output "certificate_arn" {
  value = data.aws_acm_certificate.cert.arn
}

output "alb_group_name" {
  value = local.alb_group_name
}

output "statusboard_urls" {
  description = "Public addresses of the two environments"
  value = {
    staging = "https://status-staging.${var.domain_name}"
    prod    = "https://status.${var.domain_name}"
  }
}

output "backup_bucket" {
  description = "Nightly PostgreSQL backups: s3://<bucket>/<environment>/"
  value       = module.app_environments.backup_bucket
}

output "namespaces" {
  value = module.app_environments.namespaces
}

output "grafana_url" {
  value = module.monitoring.grafana_url
}

output "prometheus_url" {
  value = module.monitoring.prometheus_url
}

output "exports_bucket" {
  description = "Monthly data exports: s3://<bucket>/<environment>/snapshot_date=YYYY-MM-DD/"
  value       = module.app_environments.exports_bucket
}

output "data_team_download_command" {
  description = "What a data-team member runs to download every production export"
  value       = "aws s3 sync s3://${module.app_environments.exports_bucket}/prod/ ./statusboard-exports/"
}

output "data_team_group" {
  value = module.app_environments.data_team_group
}
