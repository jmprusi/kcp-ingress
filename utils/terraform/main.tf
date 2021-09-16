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

variable "aws_region" {
  description = "Region where Cloud Formation is created"
  default     = "eu-west-1"
}




module "cluster-1" {
  source             = "cloudowski/minikube/aws"
  env_name           = "cluster-1-kcp"
  subnet_id          = module.vpc.public_subnets[0]
  vpc_id             = module.vpc.vpc_id
  instance_type      = "t3a.large"
  instance_disk_size = "25"
}

module "cluster-2" {
  source             = "cloudowski/minikube/aws"
  env_name           = "cluster-2-kcp"
  subnet_id          = module.vpc.public_subnets[1]
  vpc_id             = module.vpc.vpc_id
  instance_type      = "t3a.large"
  instance_disk_size = "25"
}

#####
##  OUTPUTS
#####

output "cluster-1_ip" {
  description = "IP address of the Minikube"
  value       = module.cluster-1.public_ip
}

output "cluster-2_ip" {
  description = "IP address of the Minikube"
  value       = module.cluster-2.public_ip
}

output "cluster-1_kubeconfig" {
  description = "cluster-1 kubeconfig"
  value = module.cluster-1.kubeconfig
}

output "cluster-2_kubeconfig" {
  description = "cluster-2 kubeconfig"
  value = module.cluster-2.kubeconfig
}