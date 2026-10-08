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
  default     = "https://github.com/Level-p/Statusboard-eks-helm-project.git"
}

variable "create_github_oidc_provider" {
  description = "Set to false if the account already has the token.actions.githubusercontent.com provider"
  type        = bool
  default     = true
}
