# 5. GitOps with Argo CD; CI never touches the cluster

Status: accepted

## Context

The obvious pipeline ends with `helm upgrade --install` in a CI job. It needs
cluster credentials in CI, it can put anything into the cluster, and it leaves
no record beyond a log that expires.

## Decision

CI builds, scans and signs an image, then commits the resulting digest to
`deploy/env/images/<env>/<service>.yaml`. Argo CD reconciles the cluster toward
git. CI holds no cluster credentials.

## Consequences

- The repository is the record of what is deployed. "What is running in
  staging?" is answered by reading a file at a commit.
- A rollback is `git revert`, reviewable like any other change.
- Manual drift in the cluster is corrected automatically instead of silently
  becoming the real configuration.
- A compromised CI job can push an image; it cannot reach the cluster.
- Cost: one more component to operate, and a deploy is no longer synchronous
  with the pipeline. Argo CD's sync status becomes the thing to watch.
- Production sync is manual, not because automation is untrusted, but because
  the promotion decision stays with a person until the canary analysis has a
  track record. The Rollout still gates the traffic shift once a sync begins.
