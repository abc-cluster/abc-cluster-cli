---
sidebar_position: 5
---

# secrets

Encrypted local and remote secrets storage. Secrets can be used directly in job scripts via `#ABC secrets=KEY` directives.

## secrets init

Generate local crypt material (age key pair) for the active context:

```bash
abc secrets init
```

Must be run once per context before using any other `secrets` commands.

## secrets set / get / list / delete

```bash
abc secrets set MY_KEY "hunter2"
abc secrets get MY_KEY              # → hunter2
abc secrets list
abc secrets delete MY_KEY
```

## secrets ref

Print the reference token for a secret (used in `#ABC` directives):

```bash
abc secrets ref MY_KEY
# → abc://secrets/MY_KEY
```

## secrets backend setup

Configure a remote backend (Nomad Variables, Vault):

```bash
abc secrets backend setup --type nomad
abc secrets backend setup --type vault
```

## Using secrets in jobs

Reference secrets in shell scripts via the `#ABC secrets` directive:

```bash
#!/bin/bash
#ABC secrets=MY_KEY,ANOTHER_KEY

echo "Running with $MY_KEY"
```

`abc job run` injects the decrypted values as environment variables inside the Nomad allocation.
