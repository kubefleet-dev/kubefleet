#!/usr/bin/env bash
# The cosign identity a genuine KubeFleet release signs under: OIDC issuer plus
# certificate-identity pattern.
#
# Sourced by the signing scripts and by their tests so there is exactly one
# definition. A second copy would be worse than none: the tests would assert
# their own reimplementation and keep passing after the scripts had drifted.
#
# This file is meant to be sourced, not executed.

# The issuer half is not decoration. The identity below is only a string; any
# OIDC provider could mint a certificate carrying it. Pinning the issuer is what
# makes the pattern mean "GitHub Actions said so".
# shellcheck disable=SC2034  # read by the scripts that source this file
RELEASE_OIDC_ISSUER="https://token.actions.githubusercontent.com"

# The tag shapes a release can carry, as an ERE fragment. Named here so the
# tests can check it against setup-release.yml by literal comparison instead of
# carrying a hand-escaped third copy - which is the very thing this file exists
# to avoid.
# shellcheck disable=SC2034  # read by the scripts that source this file
RELEASE_TAG_GRAMMAR='v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?'

# release_identity_pattern <owner/repo>
#
# Prints the --certificate-identity-regexp that matches a release signature and
# nothing else. Two things are bound:
#
#   The workflow file. Any other workflow that gains id-token: write could
#   otherwise mint signatures that pass; the repository already has one such
#   workflow (squad-docs.yml, for GitHub Pages).
#
#   The ref. workflow_dispatch runs the workflow *definition* from the ref it
#   was started on, so without this anyone able to push a branch and dispatch it
#   could run a modified release.yml and sign under an identity consumers trust.
#
# What this does and does not buy, stated plainly: it removes "any branch in the
# repository" from the trusted set, which is the cheap and large win. It does
# not make the identity unforgeable by someone who already has write access -
# they can still push a `v*` tag at a commit carrying a modified release.yml,
# and the tag arm accepts it. Closing that needs a GitHub ruleset restricting
# who may create `v*` tags and push to `main`/`release-*`, which is repository
# configuration rather than anything this file can enforce. Treat the pattern as
# a bound on which *workflow definitions* can sign, not as an access control.
#
# The pattern is anchored at both ends because cosign matches it as a search,
# not as a full match: unanchored, a trusted identity appearing anywhere in an
# attacker-chosen string would satisfy it.
release_identity_pattern() {
  local repo="$1"
  # The repository name is interpolated into a regexp and may contain dots,
  # which would otherwise match any character.
  local repo_pattern="${repo//./\\.}"
  # Asserted against setup-release.yml by test-release-scripts.sh: if the tag
  # grammar there widens and this does not, signing breaks after images are
  # already published.
  local tag_ref="tags/${RELEASE_TAG_GRAMMAR}"
  local branch_ref='heads/(main|release-[0-9]+\.[0-9]+)'
  # printf rather than echo: this string is mostly backslashes, and echo's
  # handling of them is implementation-defined.
  printf '^https://github\\.com/%s/\\.github/workflows/release\\.yml@refs/(%s|%s)$\n' \
    "${repo_pattern}" "${tag_ref}" "${branch_ref}"
}
