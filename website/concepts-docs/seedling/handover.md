---
title: Issuing access handover files
---

# Issuing access handover files

For named users (group leads, admins, staff) who need persistent, named
access rather than pseudonymous pool slots, you can issue a `config.yaml`
handover file directly from the CLI.

## When to use this

| Scenario | Use |
|---|---|
| A pool user redeems a claim code | Landing page download (automatic) |
| A named group admin needs full namespace write access | Handover file (this page) |
| You need to give a collaborator your own admin context | `abc auth context show yaml` |
| You want to save and share a context across machines | `abc auth context show yaml` |

## Export a context as YAML

```bash
abc auth context show yaml <context-name>
```

This prints a complete, self-contained `config.yaml` snippet that can be
handed to the recipient. All sub-fields are included: `admin.services.nomad`,
`admin.services.minio`, `capabilities`, `cluster_type`.

**Example — export the seedling-anel context:**

```bash
abc auth context show yaml seedling-anel-mbhg-hostgen-admin
```

Output (save to a file):

```yaml
# abc-cluster CLI configuration — seedling · admin · anel (mbhg-hostgen)
version: "1.0"
active_context: "seedling"
contexts:
  seedling:
    endpoint: "https://nomad.your-cluster.example.com"
    upload_endpoint: "https://upload.your-cluster.example.com/files/"
    access_token: "<nomad-token>"
    cluster_type: "abc-cluster"
    admin:
      services:
        nomad:
          addr: "https://nomad.your-cluster.example.com"
          token: "<nomad-token>"
          namespace: "su-mbhg-hostgen"
        minio:
          endpoint: "https://s3.your-cluster.example.com"
          access_key: "<minio-user>"
          secret_key: "<minio-secret>"
    capabilities:
      ...
```

Save to a file and send securely to the recipient:

```bash
abc auth context show yaml seedling-anel-mbhg-hostgen-admin \
  > handover/anel-mbhg-hostgen-admin.yaml
```

## Import a handover file

The recipient uses `abc auth context add --from-file` to merge the handover
file into their `~/.abc/config.yaml`:

```bash
# Import and name the context locally (the file's active_context is "seedling";
# --as avoids colliding with an existing seedling context):
abc auth context add \
  --from-file anel-mbhg-hostgen-admin.yaml \
  --as seedling-anel-mbhg-hostgen-admin

# Verify:
abc auth context show seedling-anel-mbhg-hostgen-admin
abc auth whoami
```

All sub-fields — including `admin.services` and `capabilities.nodes` — are
preserved exactly. The recipient can immediately run admin commands in the
correct namespace without any additional configuration.

## Naming conventions

Use a consistent naming scheme for handover contexts to avoid collisions:

```
seedling-<firstname>-<group>[-<role>]

Examples:
  seedling-anel-mbhg-hostgen-admin
  seedling-kristine-lab1-admin
  seedling-jorge-demo
```

## Handover file security

A handover file contains **live credentials** — Nomad token secret and MinIO
secret key. Treat it like a password file:

- Transmit over an encrypted channel (Signal, encrypted email, HTTPS).
- Do **not** commit handover files to git (add `handover/*.yaml` to
  `.gitignore`, keeping the directory for local use only).
- If a handover file is leaked, revoke the Nomad token and rotate the MinIO
  secret immediately:

```bash
# Revoke Nomad token (requires admin token):
nomad acl token delete -address=... -token=<admin> <accessor-id>

# Rotate MinIO secret (requires root MinIO access):
mc admin user secretkey set alias/ <minio-user> <new-secret>
```
