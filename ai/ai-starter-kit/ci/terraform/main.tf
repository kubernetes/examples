terraform {

  required_providers {
    kubectl = {
      source  = "gavinbunney/kubectl"
      version = ">= 1.19.0"
    }
  }
}
data "google_client_config" "default" {}


data "google_project" "project" {
  project_id = var.project_id
}


locals {
  cluster_name = var.cluster_name != "" ? var.cluster_name : var.default_resource_name
}

module "gke_cluster" {
  source = "github.com/ai-on-gke/common-infra/common/infrastructure?ref=main"

  project_id        = var.project_id
  cluster_name      = local.cluster_name
  cluster_location  = var.cluster_location
  autopilot_cluster = var.autopilot_cluster
  private_cluster   = var.private_cluster
  create_network    = false
  network_name      = "default"
  subnetwork_name   = "default"
  enable_gpu        = true
  gpu_pools = [
    {
      name               = "gpu-pool-l4"
      machine_type       = "g2-standard-24"
      node_locations     = "us-central1-a" ## comment to autofill node_location based on cluster_location
      autoscaling        = true
      min_count          = 1
      max_count          = 3
      disk_size_gb       = 100
      disk_type          = "pd-balanced"
      enable_gcfs        = true
      logging_variant    = "DEFAULT"
      accelerator_count  = 2
      accelerator_type   = "nvidia-l4"
      gpu_driver_version = "DEFAULT"
    }
  ]
  ray_addon_enabled = false
}

locals {
  #ca_certificate        = base64decode(module.gke_cluster.ca_certificate)
  cluster_membership_id = var.cluster_membership_id == "" ? local.cluster_name : var.cluster_membership_id
  host                  = var.private_cluster ? "https://connectgateway.googleapis.com/v1/projects/${data.google_project.project.number}/locations/${var.cluster_location}/gkeMemberships/${local.cluster_membership_id}" : "https://${module.gke_cluster.endpoint}"

}

provider "kubernetes" {
  alias                  = "ai_starter_kit"
  host                   = local.host
  token                  = data.google_client_config.default.access_token
  cluster_ca_certificate = var.private_cluster ? "" : base64decode(module.gke_cluster.ca_certificate)

  dynamic "exec" {
    for_each = var.private_cluster ? [1] : []
    content {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "gke-gcloud-auth-plugin"
    }
  }
}

locals {
  service_account_name = var.service_account_name != "" ? var.service_account_name : var.default_resource_name
}


module "ai_starter_kit_workload_identity" {
  providers = {
    kubernetes = kubernetes.ai_starter_kit
  }
  source     = "terraform-google-modules/kubernetes-engine/google//modules/workload-identity"
  name       = local.service_account_name
  namespace  = "default"
  roles      = ["roles/storage.objectUser"]
  project_id = var.project_id
  depends_on = [module.gke_cluster]
}

provider "kubectl" {
  alias                  = "ai_starter_kit"
  apply_retry_count      = 15
  host                   = local.host
  token                  = data.google_client_config.default.access_token
  cluster_ca_certificate = var.private_cluster ? "" : base64decode(module.gke_cluster.ca_certificate)
  load_config_file       = true

  dynamic "exec" {
    for_each = var.private_cluster ? [1] : []
    content {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "gke-gcloud-auth-plugin"
    }
  }
}
