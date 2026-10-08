# ---------------------------------------------------------------------------
# Everything that makes "staging" and "prod" separate environments inside one
# EKS cluster:
#   1. A namespace per environment, with Pod Security labels
#   2. One S3 bucket for database backups, a folder (prefix) per environment,
#      with its own retention period
#   3. An IAM role per environment that may ONLY write to its own folder,
#      linked to the backup job's ServiceAccount with EKS Pod Identity
# ---------------------------------------------------------------------------

data "aws_caller_identity" "current" {}

locals {
  bucket_name = "${var.name}-backups-${data.aws_caller_identity.current.account_id}"
  # Must match the Helm chart: release "statusboard" -> ServiceAccount "statusboard-backup"
  backup_service_account = "statusboard-backup"
}

# ---- 1. Namespaces ----
resource "kubernetes_namespace_v1" "env" {
  for_each = var.environments

  metadata {
    name = "statusboard-${each.key}"
    labels = {
      "app.kubernetes.io/part-of" = "statusboard"
      environment                 = each.key
      # Pod Security Standards: block privileged pods outright, and warn about
      # anything that is not "restricted" (the chart is written to pass restricted)
      "pod-security.kubernetes.io/enforce" = "baseline"
      "pod-security.kubernetes.io/warn"    = "restricted"
      "pod-security.kubernetes.io/audit"   = "restricted"
    }
  }
}

# ---- 2. Backup bucket ----
resource "aws_s3_bucket" "backups" {
  bucket        = local.bucket_name
  force_destroy = var.force_destroy_backups

  tags = { Name = local.bucket_name }
}

resource "aws_s3_bucket_public_access_block" "backups" {
  bucket                  = aws_s3_bucket.backups.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Versioning: an overwritten or deleted backup can still be recovered
resource "aws_s3_bucket_versioning" "backups" {
  bucket = aws_s3_bucket.backups.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Delete old backups automatically, per environment
resource "aws_s3_bucket_lifecycle_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id

  dynamic "rule" {
    for_each = var.environments
    content {
      id     = "expire-${rule.key}"
      status = "Enabled"
      filter {
        prefix = "${rule.key}/"
      }
      expiration {
        days = rule.value.backup_retention_days
      }
      noncurrent_version_expiration {
        noncurrent_days = 7
      }
    }
  }

  depends_on = [aws_s3_bucket_versioning.backups]
}

# ---- 3. Backup IAM roles (EKS Pod Identity) ----
resource "aws_iam_role" "backup" {
  for_each = var.environments
  name     = "${var.name}-${each.key}-backup"

  # Only the EKS Pod Identity service may assume this role
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "pods.eks.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })
}

# Least privilege: staging can never touch prod backups, and neither can delete them
resource "aws_iam_role_policy" "backup" {
  for_each = var.environments
  name     = "write-${each.key}-backups"
  role     = aws_iam_role.backup[each.key].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "WriteOwnFolder"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = "${aws_s3_bucket.backups.arn}/${each.key}/*"
      },
      {
        Sid      = "ListOwnFolder"
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = aws_s3_bucket.backups.arn
        Condition = {
          StringLike = { "s3:prefix" = ["${each.key}/*"] }
        }
      }
    ]
  })
}

# Links namespace + ServiceAccount to the IAM role. The ServiceAccount itself is
# created later by the Helm chart; the association waits for it.
resource "aws_eks_pod_identity_association" "backup" {
  for_each        = var.environments
  cluster_name    = var.cluster_name
  namespace       = kubernetes_namespace_v1.env[each.key].metadata[0].name
  service_account = local.backup_service_account
  role_arn        = aws_iam_role.backup[each.key].arn
}
