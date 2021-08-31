provider "aws" {
  region = var.aws_region
}
module "vpc" {
  source = "terraform-aws-modules/vpc/aws"

  name = "kcp-poc"
  cidr = "10.0.0.0/16"

  azs             = ["eu-west-1a", "eu-west-1b", "eu-west-1c"]
  public_subnets  = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]

  enable_dns_hostnames = true
  enable_nat_gateway = false
  enable_vpn_gateway = false

  tags = {
    Terraform = "true"
    Environment = "kcp-devel"
  }
}

resource "aws_route53_zone" "private" {
  name = "example.com"

  vpc {
    vpc_id = module.vpc.vpc_id
  }
}
variable "aws_region" {
  description = "Region where Cloud Formation is created"
  default     = "eu-west-1"
}
