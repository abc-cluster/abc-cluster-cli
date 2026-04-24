Rendered abc_nodes_enhanced jobs: traefik, abc-nodes-auth, minio, rustfs, tusd, uppy, ntfy, prometheus, loki, grafana, alloy, job-notifier, redis, postgres, docker-registry.

Recommended order: traefik -> auth -> storage (minio/rustfs) -> upload (tusd/uppy) -> notifications (ntfy/job-notifier) -> observability (prometheus/loki/grafana/alloy) -> optional data services (redis/postgres/docker-registry).

See deployments/abc-nodes/nomad/README.md for operational notes.
