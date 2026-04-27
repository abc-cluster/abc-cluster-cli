// ---------------------------------------------------------------------------
// workspace.ts — WorkspaceComponent: top-level Pulumi ComponentResource.
//
// Encapsulates all Nomad + MinIO resources for a single workspace so that
// Pulumi's resource tree groups them cleanly under one logical node.
//
// Key design:
//   • Nomad token SecretIDs double as MinIO user passwords (one credential pair
//     per identity — same convention as existing acl/ bootstrap scripts).
//   • Nomad token secrets flow from nomad.ts → minio.ts so MinIO user passwords
//     are derived from Nomad tokens, not independently generated.
//   • MinIO IAM credentials are written back to Nomad variables in the
//     abc-services namespace (path: nomad/jobs/abc-nodes-minio-iam/<principal>).
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import { OrgSpec, WorkspaceSpec } from "./types";
import { provisionNomadWorkspace, NomadWorkspaceOutputs } from "./nomad";
import { provisionMinioWorkspace, MinioWorkspaceOutputs } from "./minio";

// ---- inputs ----------------------------------------------------------------

export interface WorkspaceComponentArgs {
  org: OrgSpec;
  spec: WorkspaceSpec;
}

// ---- component -------------------------------------------------------------

export class WorkspaceComponent extends pulumi.ComponentResource {
  /** Nomad namespace name (= MinIO bucket name = resourceName). */
  public readonly namespaceName: pulumi.Output<string>;
  /** MinIO bucket name. */
  public readonly bucketName: pulumi.Output<string>;
  /**
   * Nomad ACL token secrets keyed by MinIO principal name.
   * e.g. { "su-mbhg-bioinformatics_kim": "<secret-id>", ... }
   * Pulumi Secret — never stored in plaintext state.
   */
  public readonly tokenSecrets: pulumi.Output<Record<string, string>>;
  /**
   * MinIO IAM credential records per principal.
   * { accessKey, secretKey, role, scope, bucket }
   * Pulumi Secret.
   */
  public readonly minioCredentials: pulumi.Output<Record<string, { accessKey: string; secretKey: string; role: string; scope: string; bucket: string }>>;

  constructor(
    resourceName: string,
    args: WorkspaceComponentArgs,
    opts?: pulumi.ComponentResourceOptions,
  ) {
    super("abc:userspace:Workspace", resourceName, {}, opts);

    const childOpts: pulumi.ComponentResourceOptions = {
      parent: this,
      ...opts,
    };

    // 1. Nomad resources — emits tokenSecrets (Nomad SecretIDs = MinIO passwords)
    const nomadOut: NomadWorkspaceOutputs = provisionNomadWorkspace(
      resourceName,
      args.org,
      args.spec,
      childOpts,
    );

    // 2. MinIO resources — consumes nomadOut.tokenSecrets to set MinIO passwords
    const minioOut: MinioWorkspaceOutputs = provisionMinioWorkspace(
      resourceName,
      args.org,
      args.spec,
      nomadOut.tokenSecrets,
      childOpts,
    );

    // Expose outputs
    this.namespaceName = nomadOut.namespaceName;
    this.bucketName = minioOut.bucketName;
    this.tokenSecrets = nomadOut.tokenSecrets;
    this.minioCredentials = minioOut.credentials;

    this.registerOutputs({
      namespaceName: this.namespaceName,
      bucketName: this.bucketName,
    });
  }
}
