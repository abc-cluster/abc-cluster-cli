# Node pool: infra
# Intended for nomad01 (server+client that hosts MinIO, Redis, head jobs).
# Jobs that need persistent volumes (data-minio, data-redis, jfs) run here.
#
# Apply:  nomad node-pool apply acl/node-pools/infra.hcl
#
# Then add to nomad_configs/nomad01.hcl inside client {}:
#   node_pool = "infra"

name        = "infra"
description = "Infrastructure node (nomad01): MinIO, Redis, Nextflow head jobs"

meta {
  role = "infrastructure"
}
