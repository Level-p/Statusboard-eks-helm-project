variable "name" {
  description = "Base name, e.g. statusboard-eks"
  type        = string
}

variable "cluster_name" {
  type = string
}

variable "environments" {
  description = "Map of environment name => settings. One namespace statusboard-<name> per entry."
  type = map(object({
    backup_retention_days = number
  }))
}

variable "force_destroy_backups" {
  description = "Let terraform destroy delete the backup bucket even if it still holds backups (true for labs; set false for real data)"
  type        = bool
  default     = true
}

variable "export_retention_days" {
  description = "How long monthly data exports are kept before S3 deletes them"
  type        = number
  default     = 730
}

variable "data_team_user_names" {
  description = "Existing IAM user names to put in the read-only data team group"
  type        = list(string)
  default     = []
}
