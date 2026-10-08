#!/bin/bash
# ---------------------------------------------------------------------------
# 1. Creates the S3 bucket that stores Terraform state (skipped if it exists)
# 2. Applies the bootstrap stack: GitHub OIDC role + wildcard ACM certificate
#
# State locking uses S3 lock files (use_lockfile = true), so no DynamoDB
# table is needed. Requires Terraform >= 1.10.
# ---------------------------------------------------------------------------
set -e

BUCKET_NAME="mfon21-eks-statusboard-tfstate"   # must be globally unique; also update provider.tf files
AWS_REGION="eu-west-2"
AWS_PROFILE="default"

# Create S3 bucket (skipped if it already exists and is accessible)
if aws s3api head-bucket --bucket "$BUCKET_NAME" --region "$AWS_REGION" --profile "$AWS_PROFILE" 2>/dev/null; then
  echo "S3 bucket $BUCKET_NAME already exists. Skipping creation."
else
  echo "Creating S3 bucket..."
  aws s3api create-bucket --bucket "$BUCKET_NAME" --region "$AWS_REGION" --profile "$AWS_PROFILE" \
    --create-bucket-configuration LocationConstraint="$AWS_REGION"
  echo "S3 bucket created."
fi

echo "Enabling S3 versioning..."
aws s3api put-bucket-versioning --bucket "$BUCKET_NAME" --region "$AWS_REGION" --profile "$AWS_PROFILE" \
  --versioning-configuration Status=Enabled

echo "Enabling S3 encryption..."
aws s3api put-bucket-encryption --bucket "$BUCKET_NAME" --region "$AWS_REGION" --profile "$AWS_PROFILE" \
  --server-side-encryption-configuration '{
    "Rules": [{ "ApplyServerSideEncryptionByDefault": { "SSEAlgorithm": "AES256" } }]
  }'

echo "Blocking public access..."
aws s3api put-public-access-block --bucket "$BUCKET_NAME" --region "$AWS_REGION" --profile "$AWS_PROFILE" \
  --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

# =========================
# Apply the bootstrap stack
# =========================
cd bootstrap
terraform init
terraform fmt -recursive
terraform apply -auto-approve

echo ""
echo "Remote state bucket ready and bootstrap stack applied."
echo "Add this value as the GitHub repository secret AWS_ROLE_ARN:"
terraform output -raw github_actions_role_arn
echo ""
