#!/usr/bin/env bash
#
# Lift this directory into its own repository, preserving its commit history.
#
# The project was developed inside another repository as a subdirectory, but
# everything in it — the Go module path, the chart image repositories, the
# ApplicationSet's repoURL, the GitHub OIDC trust policy — is written for a
# standalone repo at <owner>/eks-microservices-platform. The CI workflows in
# .github/ only run once this is a repository root.
#
# `git subtree split` rewrites the history of this subdirectory as if it had
# always been the root, so the commits survive rather than arriving as one
# squashed "initial commit".
set -euo pipefail

SUBDIR="eks-microservices-platform"
BRANCH="platform-extract"

info() { printf '\033[0;34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[0;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ -d .git ] || die "run this from the root of the repository that contains $SUBDIR/"
[ -d "$SUBDIR" ] || die "$SUBDIR/ not found; are you in the right repository?"

git diff --quiet && git diff --cached --quiet \
  || die "working tree is dirty; commit or stash first (subtree split reads committed state)"

info "Splitting $SUBDIR/ into the '$BRANCH' branch with its history intact"
git subtree split --prefix="$SUBDIR" -b "$BRANCH"

cat <<EOF

Done. The '$BRANCH' branch now contains $SUBDIR/ as its root.

To publish it:

  1. Create an empty repository on GitHub — no README, no .gitignore, no
     licence. Anything pre-created becomes an unrelated-histories conflict on
     the first push.

         gh repo create <owner>/eks-microservices-platform --public

  2. Push the split branch to it as main:

         git push git@github.com:<owner>/eks-microservices-platform.git $BRANCH:main

  3. Clone it fresh and confirm it stands on its own:

         git clone git@github.com:<owner>/eks-microservices-platform.git
         cd eks-microservices-platform
         make verify

Then update these references, which name the repository explicitly:

  - go.mod                                module path
  - deploy/charts/*/values.yaml           image.repository (ghcr.io/<owner>/...)
  - deploy/argocd/applicationset.yaml     repoURL and sourceRepos
  - infra/terraform/variables.tf          github_repository, which scopes the
                                          OIDC trust policy — leaving this wrong
                                          means CI cannot assume the deploy role
  - deploy/observability/slo-rules.yaml   runbook_url annotations

Finally, delete the local branch once it is pushed:

  git branch -D $BRANCH
EOF
