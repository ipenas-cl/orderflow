variable "environment" { type = string }
variable "vpc_cidr" {
  type    = string
  default = "10.40.0.0/16"
}
variable "kubernetes_version" {
  type    = string
  default = "1.31"
}
variable "node_instance_types" {
  type    = list(string)
  default = ["m6i.large"]
}
variable "node_min" {
  type    = number
  default = 3
}
variable "node_max" {
  type    = number
  default = 9
}
variable "db_instance_class" {
  type    = string
  default = "db.t4g.medium"
}
variable "tags" {
  type    = map(string)
  default = {}
}
