# Local Docker registry — push laptop-built images, pull from Nomad jobs

`abc-experimental-docker-registry` is a `registry:2` instance in the
`abc-experimental` Nomad namespace, persistent on aither's `scratch` host
volume. It's plain HTTP on `100.70.185.46:5000` (Tailscale) so two one-time
configuration steps are required before push/pull works:

1. **Laptop / dev box** must be told the registry is "insecure" (HTTP only).
2. **aither's containerd** must be told the same — otherwise Nomad jobs
   that reference `100.70.185.46:5000/<image>` will fail to pull.

After both are done, the workflow is:

```
laptop (docker push) ──► registry:5000 ──► aither containerd (nerdctl pull)
                                       └─► Nomad job (containerd-driver)
```

---

## 0. Prerequisites

- The job must already be running:

  ```bash
  cd deployments/abc-nodes/terraform
  abc admin services cli terraform -- apply -auto-approve \
    -target='nomad_job.docker_registry[0]' \
    -var='enable_docker_registry=true'
  ```

- Quick sanity check that the registry is reachable:

  ```bash
  curl -sI http://100.70.185.46:5000/v2/
  # → HTTP/1.1 200 OK
  # → Docker-Distribution-Api-Version: registry/2.0
  ```

---

## 1. Laptop side — let docker push to plain-HTTP registry

### macOS / Windows (Docker Desktop)

Settings (gear icon) → **Docker Engine** → edit the JSON to add (or extend)
the `insecure-registries` array:

```jsonc
{
  "builder": { "gc": { "defaultKeepStorage": "20GB", "enabled": true } },
  "experimental": false,
  "insecure-registries": ["100.70.185.46:5000"]
}
```

Click **Apply & Restart**.

### Linux (Docker Engine)

Edit (or create) `/etc/docker/daemon.json`:

```jsonc
{
  "insecure-registries": ["100.70.185.46:5000"]
}
```

Then:

```bash
sudo systemctl restart docker
```

### Verify

```bash
docker info | grep -A1 "Insecure Registries"
# → Insecure Registries:
# →   100.70.185.46:5000
```

---

## 2. aither side — let containerd pull from plain-HTTP registry

The cluster runs **containerd** (via Nomad's `containerd-driver`), not the
docker daemon. Containerd's registry trust is configured via per-host
config files at `/etc/containerd/certs.d/<host>:<port>/hosts.toml`.

### Option A: helper script

```bash
# From your laptop:
deployments/abc-nodes/scripts/configure-aither-registry-trust.sh
```

This script SSHes to `sun-aither` (uses your existing SSH config), writes
the `hosts.toml`, and restarts containerd.

### Option B: do it manually

```bash
ssh sun-aither
sudo mkdir -p /etc/containerd/certs.d/100.70.185.46:5000
sudo tee /etc/containerd/certs.d/100.70.185.46:5000/hosts.toml >/dev/null <<'EOF'
server = "http://100.70.185.46:5000"

[host."http://100.70.185.46:5000"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
EOF
sudo systemctl restart containerd
```

### Verify (still on aither)

```bash
sudo nerdctl --namespace nomad pull 100.70.185.46:5000/<your-image>:<tag>
# Should pull without "x509: certificate signed by unknown authority"
# or "http: server gave HTTP response to HTTPS client"
```

You only need to do step 2 once per cluster node; the config persists
across reboots.

---

## 3. Push an image

```bash
# Tag whatever you've built locally:
docker tag my-app:dev 100.70.185.46:5000/my-app:dev

# Push:
docker push 100.70.185.46:5000/my-app:dev

# Confirm it landed:
curl http://100.70.185.46:5000/v2/_catalog
# → {"repositories":["my-app"]}
curl http://100.70.185.46:5000/v2/my-app/tags/list
# → {"name":"my-app","tags":["dev"]}
```

> **Multi-arch note**: aither is `linux/amd64`. If you're building on an
> Apple Silicon Mac, build for amd64 explicitly:
>
> ```bash
> docker buildx build --platform linux/amd64 -t my-app:dev --load .
> ```
>
> Otherwise `nerdctl pull` on aither will fail with
> `no match for platform in manifest`.

---

## 4. Use the image in a Nomad jobspec

Reference the image by its registry-qualified path. The `containerd-driver`
will pull it via the trust config from step 2.

```hcl
job "my-app" {
  namespace = "abc-experimental"   # or any namespace your token can write to
  type      = "service"

  group "app" {
    network {
      mode = "bridge"
      port "http" {
        static = 18080
        to     = 8080
      }
    }

    task "app" {
      driver = "containerd-driver"
      config {
        image = "100.70.185.46:5000/my-app:dev"
      }
      resources {
        cpu    = 100
        memory = 256
      }
    }
  }
}
```

Submit:

```bash
NOMAD_NAMESPACE=abc-experimental \
  abc admin services nomad cli -- job run my-app.nomad.hcl
```

For a fresh tag to be picked up, push first then `nomad job run` will see
the new manifest digest. Containerd caches by digest, so re-pushing the
same `:dev` tag with new content does cause a re-pull on the next alloc.

---

## 5. Inspect / clean up

```bash
# List repos:
curl http://100.70.185.46:5000/v2/_catalog

# List tags for one repo:
curl http://100.70.185.46:5000/v2/my-app/tags/list

# Get the manifest digest (needed for delete):
curl -sI \
  -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
  http://100.70.185.46:5000/v2/my-app/manifests/dev | grep -i docker-content-digest
# → docker-content-digest: sha256:abc123…

# Delete the manifest by digest (the registry has REGISTRY_STORAGE_DELETE_ENABLED=true):
curl -X DELETE http://100.70.185.46:5000/v2/my-app/manifests/sha256:abc123…
```

The `DELETE` call only marks the manifest as deleted; the underlying blobs
are reclaimed by a separate **garbage-collect** pass:

```bash
ALLOC=$(NOMAD_NAMESPACE=abc-experimental \
  abc admin services nomad cli -- job status -short \
    abc-experimental-docker-registry | awk '/registry/ && /running/ {print $1; exit}')

NOMAD_NAMESPACE=abc-experimental \
  abc admin services nomad cli -- alloc exec -task registry "$ALLOC" \
    registry garbage-collect /etc/docker/registry/config.yml
```

To wipe the registry completely (start over):

```bash
abc admin services nomad cli -- job stop -purge abc-experimental-docker-registry
ssh sun-aither sudo rm -rf /opt/nomad/scratch/docker-registry
abc admin services cli terraform -- apply -auto-approve \
  -target='nomad_job.docker_registry[0]' \
  -var='enable_docker_registry=true'
```

---

## 6. Troubleshooting

| Symptom (laptop) | Cause | Fix |
|---|---|---|
| `http: server gave HTTP response to HTTPS client` | Daemon doesn't trust HTTP registry | Add to `insecure-registries` (§1), restart |
| `denied: requested access to the resource is denied` on `docker push` | This registry has no auth, but the daemon may be sending stale Docker Hub creds | `docker logout 100.70.185.46:5000` then retry |
| `no match for platform "linux/amd64" in manifest` on `nerdctl pull` | You pushed an arm64 image from Apple Silicon | Rebuild with `docker buildx build --platform linux/amd64` |
| `manifest unknown` on `nerdctl pull` for a tag you just pushed | Tag was pushed but `:latest` was assumed; check the actual tag | `curl /v2/<repo>/tags/list` |

| Symptom (aither / Nomad job) | Cause | Fix |
|---|---|---|
| Job stuck in pending; alloc events show `failed to resolve reference … http: server gave HTTP response to HTTPS client` | Containerd not trusting the registry | Step 2 (the `hosts.toml`) |
| Image pulls slowly / times out | aither has many concurrent pulls (e.g. supabase startup) | Just retry — pulls are sequential on this single-node cluster |
| Push works but pull on aither shows `no such manifest` | The image was pushed for a different platform | Same multi-arch fix as above |

---

## 7. Where this is configured in the repo

| File | Role |
|---|---|
| `deployments/abc-nodes/nomad/experimental/docker-registry.nomad.hcl.tftpl` | Job spec (bridge network, scratch volume_mount, registry:2 image) |
| `deployments/abc-nodes/terraform/main.tf` (`nomad_job.docker_registry`) | Terraform resource that templates the spec and registers it |
| `deployments/abc-nodes/terraform/variables.tf` | `enable_docker_registry`, `docker_registry_image`, `docker_registry_node`, `docker_registry_port` |
| `deployments/abc-nodes/terraform/outputs.tf` | `experimental_endpoints.docker_registry` (push command + vhost URL) |
| `deployments/abc-nodes/scripts/configure-aither-registry-trust.sh` | One-shot script for §2 |
