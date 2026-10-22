output "cluster_name" { value = module.eks.cluster_name }
output "cluster_endpoint" { value = module.eks.cluster_endpoint }
output "db_endpoint" { value = module.rds.db_instance_endpoint }
output "db_master_secret_arn" { value = module.rds.db_instance_master_user_secret_arn }
