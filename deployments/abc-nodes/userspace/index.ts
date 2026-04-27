// ---------------------------------------------------------------------------
// index.ts — Pulumi entry point for abc-userspace.
//
// Reads workspaces.yaml (or the path set in `pulumi config set specFile ...`),
// iterates over orgs → workspaces, and instantiates a WorkspaceComponent for
// each active / non-deleted workspace.
//
// Quick-start:
//   cd deployments/abc-nodes/userspace
//   npm install
//   npm run build
//   pulumi stack init dev
//   pulumi config set nomadAddress http://nomad.example.com:4646
//   pulumi config set minioEndpoint minio.example.com:9000
//   pulumi config set --secret nomadToken <management-token>
//   pulumi config set --secret minioAccessKey <access-key>
//   pulumi config set --secret minioSecretKey <secret-key>
//   pulumi up
//
// To add a workspace: edit workspaces.yaml, run `pulumi up`.
// To remove a workspace: delete the entry from workspaces.yaml and run
//   `pulumi up` (Nomad/MinIO resources are destroyed by Pulumi diff).
// To suspend: change state to "suspended" and run `pulumi up`.
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as fs from "fs";
import * as path from "path";
import * as yaml from "js-yaml";

import { WorkspacesSpec, ResolvedWorkspace } from "./types";
import { WorkspaceComponent } from "./workspace";

// ---- configuration ---------------------------------------------------------

const config = new pulumi.Config();

// Path to the workspace YAML spec.
// Relative paths are resolved from CWD — Pulumi sets CWD to the project
// root (where Pulumi.yaml lives), so relative paths work as expected.
// __dirname resolves to bin/ when compiled and must NOT be used for data files.
const specFilePath = config.get("specFile") ?? "workspaces.yaml";
const resolvedSpecPath = path.isAbsolute(specFilePath)
  ? specFilePath
  : path.resolve(process.cwd(), specFilePath);

// ---- load spec -------------------------------------------------------------

function loadSpec(filePath: string): WorkspacesSpec {
  if (!fs.existsSync(filePath)) {
    throw new Error(
      `Workspace spec file not found: ${filePath}\n` +
        `Set the path with: pulumi config set specFile /path/to/workspaces.yaml`,
    );
  }
  const raw = fs.readFileSync(filePath, "utf8");
  const parsed = yaml.load(raw) as WorkspacesSpec;

  if (!parsed || parsed.version !== "v1") {
    throw new Error(
      `Invalid workspace spec: expected version: v1, got ${parsed?.version}`,
    );
  }
  return parsed;
}

const spec = loadSpec(resolvedSpecPath);

// ---- resolve workspaces ----------------------------------------------------

/**
 * Expands org → workspace into a flat list of ResolvedWorkspace records,
 * filtering out any with state=deleted (those should have been destroyed
 * already; they're kept in YAML as a tombstone until cleanup is confirmed).
 */
function resolveWorkspaces(spec: WorkspacesSpec): ResolvedWorkspace[] {
  const resolved: ResolvedWorkspace[] = [];
  for (const org of spec.organizations) {
    for (const ws of org.workspaces) {
      if (ws.state === "deleted") {
        pulumi.log.info(
          `Skipping ${org.name}-${ws.name} (state=deleted); remove from YAML after confirming destruction.`,
        );
        continue;
      }
      resolved.push({
        resourceName: `${org.name}-${ws.name}`,
        org,
        spec: ws,
      });
    }
  }
  return resolved;
}

const workspaces = resolveWorkspaces(spec);

// ---- instantiate components ------------------------------------------------

const workspaceComponents: Record<string, WorkspaceComponent> = {};

for (const ws of workspaces) {
  workspaceComponents[ws.resourceName] = new WorkspaceComponent(
    ws.resourceName,
    { org: ws.org, spec: ws.spec },
  );
}

// ---- stack outputs ---------------------------------------------------------

/**
 * Export a summary map so `pulumi stack output` gives a quick overview.
 *
 * Example:
 *   pulumi stack output workspaces
 *   {
 *     "su-mbhg-bioinformatics": { namespace: "su-mbhg-bioinformatics", bucket: "su-mbhg-bioinformatics" },
 *     ...
 *   }
 */
export const workspaceSummary = pulumi.all(
  Object.fromEntries(
    Object.entries(workspaceComponents).map(([name, comp]) => [
      name,
      pulumi.all([comp.namespaceName, comp.bucketName]).apply(
        ([namespace, bucket]) => ({ namespace, bucket }),
      ),
    ]),
  ),
);

/**
 * Nomad ACL token names per workspace (non-secret; useful for auditing which
 * principals exist). The actual SecretIDs are Pulumi Secrets and not exported.
 *
 *   pulumi stack output workspaceSummary --json
 */
