output "backup_bucket" {
  value = aws_s3_bucket.backups.bucket
}

output "namespaces" {
  value = { for k, ns in kubernetes_namespace_v1.env : k => ns.metadata[0].name }
}

output "backup_role_arns" {
  value = { for k, r in aws_iam_role.backup : k => r.arn }
}

output "exports_bucket" {
  value = aws_s3_bucket.exports.bucket
}

output "data_team_group" {
  value = aws_iam_group.data_team.name
}

output "data_team_policy_arn" {
  description = "Attach to an SSO permission set or role if your data team does not use IAM users"
  value       = aws_iam_policy.data_team_read.arn
}
