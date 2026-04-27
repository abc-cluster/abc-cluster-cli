// ---------------------------------------------------------------------------
// workspace.ts — WorkspaceComponent: per-workspace shared resources only.
//
// In v2 a workspace owns the shared bits (namespace, bucket, ACL/IAM
// policies, submit account). Per-user resources (Nomad token, IAM user,
// policy attachments) are owned by the top-level UserComponent so a
// multi-workspace user has a single principal across all workspaces.
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import { OrgSpec, WorkspaceSpec, UserSpec } from "./types";
import { provisionWorkspaceNomad, WorkspaceNomadOutputs } from "./nomad";
import { provisionWorkspaceMinio, WorkspaceMinioOutputs } from "./minio";
import { WorkspaceIamHandles } from "./user";
import { iamVarPath } from "./naming";
import * as nomad from "@pulumi/nomad";

export interface WorkspaceComponentArgs {
  org: OrgSpec;
  spec: WorkspaceSpec;
  /** Map of all top-level users (for collab policy generation). */
  users: Map<string, UserSpec>;
}

export class WorkspaceComponent extends pulumi.ComponentResource {
  public readonly namespaceName: pulumi.Output<string>;
  public readonly bucketName: pulumi.Output<string>;
  /** IAM/ACL policy resources surfaced to UserComponent for attachment. */
  public readonly handles: WorkspaceIamHandles;

  constructor(
    resourceName: string,
    args: WorkspaceComponentArgs,
    opts?: pulumi.ComponentResourceOptions,
  ) {
    super("abc:userspace:Workspace", resourceName, {}, opts);
    const childOpts: pulumi.ComponentResourceOptions = { parent: this, ...opts };

    // 1. Nomad — namespace, ACL policies, optional submit token
    const nomadOut: WorkspaceNomadOutputs = provisionWorkspaceNomad(
      resourceName,
      args.org,
      args.spec,
      childOpts,
    );

    // 2. RustFS — bucket, IAM policies, optional submit IAM user
    // submit IAM user password = submit Nomad token SecretID (one credential
    // pair per identity, same convention as v1).
    const submitPassword: pulumi.Output<string> = nomadOut.submitToken
      ? pulumi.secret(nomadOut.submitToken.secretId)
      : pulumi.output("");
    const minioOut: WorkspaceMinioOutputs = provisionWorkspaceMinio(
      resourceName,
      args.org,
      args.spec,
      args.users,
      submitPassword,
      childOpts,
    );

    // 3. If the workspace has a submit account, write its IAM credential var.
    if (minioOut.submit && nomadOut.submitToken) {
      const submitScope = "pipelines/rw+samplesheets/ro+shared/ro";
      const principal = minioOut.submit.principal;
      new nomad.Variable(
        `${resourceName}-submit-iamvar`,
        {
          namespace: new pulumi.Config().get("nomadIamNamespace") ?? "abc-services",
          path: iamVarPath(
            new pulumi.Config().get("nomadIamVarPrefix") ?? "nomad/jobs/abc-nodes-minio-iam",
            principal,
          ),
          itemsWo: pulumi.interpolate`{"access_key":"${principal}","secret_key":"${minioOut.submit.password}","role":"submit","scope":"${submitScope}","bucket":"${resourceName}"}`,
          itemsWoVersion: 1,
        },
        { ...childOpts, additionalSecretOutputs: ["itemsWo"], dependsOn: [minioOut.submit.ready] },
      );
    }

    this.namespaceName = nomadOut.namespaceName;
    this.bucketName = minioOut.bucketName;
    this.handles = {
      groupAdminMinio: minioOut.groupAdminPolicy,
      memberMinio: minioOut.memberPolicy,
      collabMinio: minioOut.collabPolicies,
      groupAdminNomad: nomadOut.groupAdminPolicy,
      memberNomad: nomadOut.memberPolicy,
    };

    this.registerOutputs({
      namespaceName: this.namespaceName,
      bucketName: this.bucketName,
    });
  }
}
