# Node pool: compute
# Intended for nomad02, nomad03, nomad04 (pure compute workers).
# Nextflow task jobs land here; no persistent service volumes required.
#
# Apply:  nomad node-pool apply acl/node-pools/compute.hcl
#
# Then add to nomad_configs/nomad02.hcl, nomad03.hcl, nomad04.hcl
# inside client {}:
#   node_pool = "compute"
#
# Finally update namespace node_pool_config in acl/namespaces/*.hcl:
#   node_pool_config {
#     default = "compute"
#     allowed = ["compute"]
#   }

name        = "compute"
description = "Compute workers (nomad02-04): Nextflow pipeline tasks"

meta {
  role = "compute"
}
