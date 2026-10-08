variable "region" {
  description = "AWS region"
  type        = string
  default     = "eu-west-2"
}

variable "domain_name" {
  description = "The primary domain name for the Route 53 hosted zone"
  type        = string
  default     = "mfon21.space"
}

variable "kubernetes_version" {
  description = "EKS Kubernetes version (check: aws eks describe-cluster-versions --region eu-west-2)"
  type        = string
  default     = "1.35"
}

variable "admin_principal_arns" {
  description = <<-EOT
    Extra IAM user/role ARNs that get cluster-admin on EKS.
    The GitHub Actions role from the bootstrap stack is added automatically.
    If you run Terraform from your laptop, put YOUR IAM ARN here,
    e.g. ["arn:aws:iam::123456789012:user/steven"].
  EOT
  type        = list(string)
  default     = ["arn:aws:iam::127214197057:user/levelp"]                                                                                   "]
}

variable "node_instance_types" {
  type    = list(string)
  default = ["t3.large"]
}

variable "grafana_admin_password" {
  description = "Grafana admin password. Supplied by the GitHub secret GRAFANA_ADMIN_PASSWORD (TF_VAR_grafana_admin_password)."
  type        = string
  sensitive   = true
}

variable "expose_prometheus" {
  description = "Publish Prometheus on https://prometheus.<domain> (it has no login, so keep false unless you add auth)"
  type        = bool
  default     = false
}

variable "nat_gateway_per_az" {
  description = "One NAT Gateway per Availability Zone (true, recommended for production) or one shared NAT Gateway (false, cheaper for labs)"
  type        = bool
  default     = false
}

variable "environments" {
  description = <<-EOT
    The application environments. Each gets a namespace (statusboard-<key>), a folder
    in the backup bucket and its own backup IAM role. backup_retention_days controls
    how long nightly database backups are kept.
  EOT
  type = map(object({
    backup_retention_days = number
  }))
  default = {
    staging = { backup_retention_days = 7 }
    prod    = { backup_retention_days = 30 }
  }
}

variable "data_team_user_names" {
  description = "Existing IAM user names allowed to download production data exports (read-only)"
  type        = list(string)
  default     = []
}
