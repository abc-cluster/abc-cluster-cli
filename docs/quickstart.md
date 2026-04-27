---
sidebar_position: 3
---

# Quick start

Five minutes to your first job. You need `abc` on your `$PATH` ([Installation](./installation)) and values from your cluster operator.

## 1. Initialize config

```bash
abc config init          # creates ~/.abc/config.yaml if not present
```

## 2. Add a context

A **context** holds the API endpoint, tokens, and workspace label for one cluster.

```bash
abc context add dev \
  --url https://api.abc-cluster.io \
  --access-token <your-token> \
  --workspace  <workspace-id>
```

Make it active:

```bash
export ABC_ACTIVE_CONTEXT=dev
# or set it persistently:
abc config set active_context dev
```

Verify:

```bash
abc auth whoami
```

## 3. Initialize local secrets

Local crypt material lets `abc secrets` and `abc data encrypt` work without exporting a password every time:

```bash
abc secrets init
abc secrets set MY_KEY "hunter2"
abc secrets get MY_KEY          # → hunter2
```

## 4. Upload a file

```bash
echo "hello abc" > sample.txt
abc data upload sample.txt
```

The upload uses TUS (resumable). Large files survive network interruptions and resume where they left off.

## 5. Submit a job

Annotate a shell script with `#ABC` directives and submit it:

```bash
cat > hello.sh << 'EOF'
#!/bin/bash
#ABC job_name=hello
#ABC image=ubuntu:24.04
echo "Hello from Nomad!"
EOF

abc job run hello.sh
abc job logs hello
```

## Next steps

- [Tutorials](./tutorials) — full hands-on walkthrough with 8 exercises
- [Reference → job run](./reference/jobs) — all `#ABC` directives and flags
- [Reference → data](./reference/data) — encrypt, upload, download
