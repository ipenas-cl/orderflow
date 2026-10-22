terraform {
  # backend "s3" { bucket = "your-tf-state" key = "orderflow/dev.tfstate" region = "us-east-1" dynamodb_table = "tf-locks" }
}

provider "aws" { region = "us-east-1" }

module "platform" {
  source      = "../../modules/platform"
  environment = "dev"
  tags        = { Owner = "platform" }
}
