# ---------------------------------------------------------------------------
# Monthly data exports for the data team
#
# On the 1st of every month the "statusboard-export" CronJob (Helm chart)
# writes a snapshot of the database to:
#   s3://<exports bucket>/<environment>/snapshot_date=YYYY-MM-DD/
#       tables/<table>.csv.gz      one CSV per table, ready for pandas, Excel, Athena...
#       full/statusboard.dump      complete pg_dump, restorable with pg_restore
#       manifest.json              when, which environment, row counts
#
# Security:
#   - its own bucket (separate from operational backups), never public
#   - encrypted with a dedicated, rotating KMS key
#   - HTTPS-only (any plain-HTTP request is denied)
#   - the export job can only ADD files to its own environment's folder
#   - the data team can only READ production exports
# ---------------------------------------------------------------------------

locals {
  exports_bucket_name    = "${var.name}-data-exports-${data.aws_caller_identity.current.account_id}"
  export_service_account = "statusboard-export"
}

resource "aws_kms_key" "exports" {
  description             = "${var.name} monthly data exports"
  enable_key_rotation     = true
  deletion_window_in_days = 7
}

resource "aws_kms_alias" "exports" {
  name          = "alias/${var.name}-data-exports"
  target_key_id = aws_kms_key.exports.key_id
}

resource "aws_s3_bucket" "exports" {
  bucket        = local.exports_bucket_name
  force_destroy = var.force_destroy_backups
  tags          = { Name = local.exports_bucket_name }
}

resource "aws_s3_bucket_ownership_controls" "exports" {
  bucket = aws_s3_bucket.exports.id
  rule {
    object_ownership = "BucketOwnerEnforced" # no ACLs: access is controlled only by IAM
  }
}

resource "aws_s3_bucket_public_access_block" "exports" {
  bucket                  = aws_s3_bucket.exports.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "exports" {
  bucket = aws_s3_bucket.exports.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.exports.arn
    }
    bucket_key_enabled = true # fewer KMS calls, lower cost
  }
}

resource "aws_s3_bucket_versioning" "exports" {
  bucket = aws_s3_bucket.exports.id
  versioning_configuration {
    status = "Enabled"
  }
}

# Cheaper storage after 90 days, deleted after export_retention_days
resource "aws_s3_bucket_lifecycle_configuration" "exports" {
  bucket = aws_s3_bucket.exports.id
  rule {
    id     = "age-out-exports"
    status = "Enabled"
    filter {}
    transition {
      days          = 90
      storage_class = "STANDARD_IA"
    }
    expiration {
      days = var.export_retention_days
    }
    noncurrent_version_expiration {
      noncurrent_days = 30
    }
  }
  depends_on = [aws_s3_bucket_versioning.exports]
}

# Refuse any request that does not use HTTPS
resource "aws_s3_bucket_policy" "exports" {
  bucket = aws_s3_bucket.exports.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "HttpsOnly"
      Effect    = "Deny"
      Principal = "*"
      Action    = "s3:*"
      Resource  = [aws_s3_bucket.exports.arn, "${aws_s3_bucket.exports.arn}/*"]
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
  depends_on = [aws_s3_bucket_public_access_block.exports]
}

# ---- Writers: the export job in each environment (EKS Pod Identity) ----
resource "aws_iam_role" "export" {
  for_each = var.environments
  name     = "${var.name}-${each.key}-export"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "pods.eks.amazonaws.com" }
      Action    = ["sts:AssumeRole", "sts:TagSession"]
    }]
  })
}

resource "aws_iam_role_policy" "export" {
  for_each = var.environments
  name     = "write-${each.key}-exports"
  role     = aws_iam_role.export[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "AddFilesToOwnFolder"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = "${aws_s3_bucket.exports.arn}/${each.key}/*"
      },
      {
        # GenerateDataKey to encrypt; Decrypt is needed for large (multipart) uploads
        Sid      = "UseExportKey"
        Effect   = "Allow"
        Action   = ["kms:GenerateDataKey", "kms:Decrypt"]
        Resource = aws_kms_key.exports.arn
      }
    ]
  })
}

resource "aws_eks_pod_identity_association" "export" {
  for_each        = var.environments
  cluster_name    = var.cluster_name
  namespace       = kubernetes_namespace_v1.env[each.key].metadata[0].name
  service_account = local.export_service_account
  role_arn        = aws_iam_role.export[each.key].arn
}

# ---- Readers: the data team (read-only, production exports only) ----
resource "aws_iam_policy" "data_team_read" {
  name        = "${var.name}-data-team-read-exports"
  description = "Download StatusBoard production data exports"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ListProdExports"
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = aws_s3_bucket.exports.arn
        Condition = {
          StringLike = { "s3:prefix" = ["prod/", "prod/*"] }
        }
      },
      {
        Sid      = "ReadProdExports"
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "${aws_s3_bucket.exports.arn}/prod/*"
      },
      {
        Sid      = "DecryptExports"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = aws_kms_key.exports.arn
      }
    ]
  })
}

resource "aws_iam_group" "data_team" {
  name = "${var.name}-data-team"
}

resource "aws_iam_group_policy_attachment" "data_team" {
  group      = aws_iam_group.data_team.name
  policy_arn = aws_iam_policy.data_team_read.arn
}

# Adds existing IAM users to the group (leave the list empty to manage membership elsewhere)
resource "aws_iam_group_membership" "data_team" {
  count = length(var.data_team_user_names) > 0 ? 1 : 0
  name  = "${var.name}-data-team-members"
  group = aws_iam_group.data_team.name
  users = var.data_team_user_names
}
