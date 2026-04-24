Rendered abc_nodes_enhanced jobs: traefik, abc-nodes-auth, minio, rustfs, tusd, uppy, ntfy, prometheus, loki, grafana, alloy, job-notifier, docker-registry.

Recommended order: traefik -> auth -> storage (minio/rustfs) -> upload (tusd/uppy) -> notifications (ntfy/job-notifier) -> observability (prometheus/loki/grafana/alloy) -> support services (docker-registry).

See deployments/abc-nodes/nomad/README.md for operational notes.
