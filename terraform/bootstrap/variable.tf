variable "domain" {
  description = "Public Route 53 hosted zone you own"
  type        = string
  default     = "mfon21.space"
}

variable "region" {
  type    = string
  default = "eu-west-2"
}

variable "github_repository" {
  description = "GitHub repository allowed to deploy, in OWNER/REPO form"
  type        = string
  default     = "Level-p/Statusboard-eks-helm-project"
}

variable "github_subject_prefix" {
  description = <<-EOT
    The OIDC subject prefix GitHub uses for your repository when "immutable subjects" are on
    (repo:OWNER@ownerId/REPO@repoId). Find it with:
    curl -s https://api.github.com/repos/OWNER/REPO/actions/oidc/customization/sub
    and copy "sub_claim_prefix". Leave empty if that shows "use_immutable_subject": false.
  EOT
  type        = string
  default     = "repo:Level-p@106237925/Statusboard-eks-helm-project@1410920280"
}

variable "create_github_oidc_provider" {
  description = "Set to false if the account already has the token.actions.githubusercontent.com provider"
  type        = bool
  default     = true
}
