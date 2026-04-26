terraform {
  required_version = ">= 1.0"

  required_providers {
    nomad = {
      source  = "hashicorp/nomad"
      version = "~> 2.3"
    }
  }

  # Local backend - for production, consider remote backend (Consul, S3, etc.)
  backend "local" {
    path = "terraform.tfstate"
  }
}

# Nomad provider configuration
# Credentials sourced from environment variables or variables
provider "nomad" {
  address = var.nomad_address
  region  = var.nomad_region

  # Token can be set via NOMAD_TOKEN env var or var.nomad_token
  # For security, prefer environment variable over hardcoded value
  secret_id = var.nomad_token != "" ? var.nomad_token : null
}
