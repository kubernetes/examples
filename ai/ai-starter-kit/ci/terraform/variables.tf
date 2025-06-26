variable "project_id" {
  type = string
}
variable "default_resource_name" {
  type = string
}
variable "cluster_name" {
  type = string
}
variable "cluster_location" {
  type = string
}
variable "autopilot_cluster" {
  type = bool
}
variable "private_cluster" {
  type = bool
}
variable "cluster_membership_id" {
  type        = string
  description = "require to use connectgateway for private clusters, default: cluster_name"
  default     = ""
}
variable "service_account_name" {
  type = string
}
