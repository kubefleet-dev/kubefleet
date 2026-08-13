#!/usr/bin/env bash
# Exercise the release scripts against a stubbed gh CLI.
#
# These scripts decide whether a release goes public, so their failure modes
# matter more than most: the cases below are the ones where getting it wrong
# publishes something wrong rather than just failing the run.
#
# Run directly: ./hack/release/test-release-scripts.sh
# Requires: bash, jq. No test framework.

set -uo pipefail

cd "$(dirname "$0")" || exit 1
here="${PWD}"
export PATH="${here}/testdata:${PATH}"

create_script="${here}/create-draft-release.sh"
publish_script="${here}/publish-release.sh"
sign_images_script="${here}/sign-images.sh"
sign_bundle_script="${here}/sign-crd-bundle.sh"

passed=0
failed=0

# Every case runs one script with a fresh stub log, then asserts on its exit
# code, its output, and the gh commands it issued.
run_case() {
  FAKE_GH_LOG="$(mktemp)"
  export FAKE_GH_LOG
  output=""
  rc=0
  output="$(env "$@" 2>&1)" || rc=$?
  log="$(cat "${FAKE_GH_LOG}")"
  rm -f "${FAKE_GH_LOG}"
}

ok() {
  echo "  PASS  $1"
  passed=$((passed + 1))
}

bad() {
  echo "  FAIL  $1"
  echo "        rc=${rc}"
  echo "        output: ${output}"
  echo "        gh calls: $(tr '\n' '|' <<<"${log}")"
  failed=$((failed + 1))
}

expect_rc() { # expect_rc <want> <description>
  if [ "${rc}" = "$1" ]; then ok "$2 (rc=${rc})"; else bad "$2 - want rc=$1"; fi
}

expect_gh() { # expect_gh <substring> <description>
  if grep -qF -- "$1" <<<"${log}"; then ok "$2"; else bad "$2 - no gh call matching '$1'"; fi
}

expect_no_gh() { # expect_no_gh <substring> <description>
  if grep -qF -- "$1" <<<"${log}"; then bad "$2 - unexpected gh call '$1'"; else ok "$2"; fi
}

expect_output() { # expect_output <substring> <description>
  if grep -qF -- "$1" <<<"${output}"; then ok "$2"; else bad "$2 - output lacks '$1'"; fi
}

echo "== create-draft-release.sh =="

run_case FAKE_GH_STATE=absent TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 0 "no existing release, stable: succeeds"
expect_gh "gh release create v0.4.0 --title v0.4.0 --generate-notes --draft --verify-tag" \
  "no existing release, stable: creates a verified draft"
expect_no_gh "--prerelease" "stable release is not flagged as a pre-release"

run_case FAKE_GH_STATE=absent TAG=v0.4.0-rc.1 PRERELEASE=true bash "${create_script}"
expect_rc 0 "no existing release, RC: succeeds"
expect_gh "--draft --verify-tag --prerelease" "RC is created as a pre-release"

run_case FAKE_GH_STATE=draft TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 0 "existing draft: succeeds"
expect_no_gh "release create" "existing draft is reused, not recreated"

run_case FAKE_GH_STATE=published TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 1 "already-published release: refuses"
expect_output "::error::" "already-published release: annotates the failure"
expect_no_gh "release create" "already-published release: creates nothing"

# A GitHub outage must not be read as "the release does not exist" - that would
# take the create path and step over the published-release guard above.
run_case FAKE_GH_STATE=absent FAKE_GH_ERROR="HTTP 503: Service unavailable" \
  TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 1 "API error that is not a 404: fails closed"
expect_no_gh "release create" "API error: creates nothing"

echo "== publish-release.sh =="

all_uploaded="$(printf 'kubefleet-crds-v0.4.0.tgz;uploaded;4096\nkubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.sha256.bundle;uploaded;2048')"

run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="${all_uploaded}" TAG=v0.4.0 PRERELEASE=false \
  bash "${publish_script}"
expect_rc 0 "complete draft, stable: publishes"
expect_gh "gh release edit v0.4.0 --draft=false --prerelease=false" \
  "stable release is published without the pre-release flag"

run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0-rc.1.tgz;uploaded;4096\nkubefleet-crds-v0.4.0-rc.1.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0-rc.1.tgz.sha256.bundle;uploaded;2048')" \
  TAG=v0.4.0-rc.1 PRERELEASE=true bash "${publish_script}"
expect_rc 0 "complete draft, RC: publishes"
# A draft created by hand defaults to prerelease=false, so the flag has to be
# set at publish time or an RC becomes the repository's "Latest release".
expect_gh "--draft=false --prerelease=true" "RC is published flagged as a pre-release"

run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="kubefleet-crds-v0.4.0.tgz;uploaded;4096" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "missing checksum asset: refuses to publish"
expect_no_gh "release edit" "missing checksum asset: release stays a draft"

run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="" TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "no assets at all: refuses to publish"

# GitHub keeps an asset row for an upload that never finished; it is present by
# name but not in the "uploaded" state.
run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0.tgz;new;0\nkubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.sha256.bundle;uploaded;2048')" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "interrupted upload (state != uploaded): refuses to publish"
expect_no_gh "release edit" "interrupted upload: release stays a draft"

run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0.tgz;uploaded;0\nkubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.sha256.bundle;uploaded;2048')" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "zero-byte asset: refuses to publish"

# Names alone are not proof; the bundle ships a checksum, so it gets checked.
run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="${all_uploaded}" FAKE_GH_DOWNLOAD=corrupt \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "bundle that fails its own checksum: refuses to publish"
expect_no_gh "release edit" "failed checksum: release stays a draft"

# An asset whose name only looks right must not satisfy the check.
run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.asc;uploaded;800')" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "similar-but-wrong asset names: refuses to publish"

echo "== sign-images.sh =="

# buildx writes one metadata file per image; "containerimage.digest" in it is
# the only record of what this run actually pushed.
write_metadata() { # write_metadata <dir> <image> <digest|"">
  mkdir -p "$1"
  if [ -n "$3" ]; then
    printf '{"containerimage.digest":"%s","image.name":"ghcr.io/x/%s"}\n' "$3" "$2" >"$1/$2.json"
  else
    printf '{"image.name":"ghcr.io/x/%s"}\n' "$2" >"$1/$2.json"
  fi
}

hub_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111"
member_digest="sha256:2222222222222222222222222222222222222222222222222222222222222222"
token_digest="sha256:3333333333333333333333333333333333333333333333333333333333333333"

meta_dir="$(mktemp -d)"
write_metadata "${meta_dir}" hub-agent "${hub_digest}"
write_metadata "${meta_dir}" member-agent "${member_digest}"
write_metadata "${meta_dir}" refresh-token "${token_digest}"
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet bash "${sign_images_script}"
expect_rc 0 "metadata for every built image: signs successfully"
expect_gh "cosign sign --recursive --yes ghcr.io/kubefleet-dev/kubefleet/hub-agent@${hub_digest}" \
  "signs the hub-agent digest this run pushed"
expect_gh "cosign sign --recursive --yes ghcr.io/kubefleet-dev/kubefleet/member-agent@${member_digest}" \
  "signs the member-agent digest this run pushed"
# Driving the loop off the metadata directory is what makes "everything built
# gets signed" structural: a hand-maintained list could drop this one silently.
expect_gh "cosign sign --recursive --yes ghcr.io/kubefleet-dev/kubefleet/refresh-token@${token_digest}" \
  "signs every image present in the metadata directory"
# Signing a tag would sign whatever it resolves to at signing time, not the
# bytes this run built.
expect_no_gh "ghcr.io/kubefleet-dev/kubefleet/hub-agent:" "never signs by tag"
expect_gh "cosign verify" "verifies each signature it creates"
rm -rf "${meta_dir}"

meta_dir="$(mktemp -d)"
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet bash "${sign_images_script}"
expect_rc 1 "no metadata at all: fails rather than signing nothing"
expect_no_gh "cosign sign" "no metadata: signs nothing"
rm -rf "${meta_dir}"

meta_dir="$(mktemp -d)"
write_metadata "${meta_dir}" hub-agent ""
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet bash "${sign_images_script}"
expect_rc 1 "metadata without a digest: fails rather than signing something else"
expect_no_gh "cosign sign" "no digest: signs nothing"
rm -rf "${meta_dir}"

# An unverifiable signature is worse than none, because the docs tell users to
# rely on it - so a failed verification has to fail the release.
meta_dir="$(mktemp -d)"
write_metadata "${meta_dir}" hub-agent "${hub_digest}"
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet FAKE_COSIGN_FAIL=verify \
  bash "${sign_images_script}"
expect_rc 1 "signature that will not verify: fails the release"
rm -rf "${meta_dir}"

echo "== signing identity pattern =="

# The identity regexp is the whole security value of keyless signing: it is what
# a consumer pins to. These assert the exact string both scripts build.
identity_pattern_for() { # identity_pattern_for <owner/repo>
  GITHUB_REPOSITORY="$1" bash -c \
    'repo_pattern="${GITHUB_REPOSITORY//./\\.}"; echo "^https://github\.com/${repo_pattern}/\.github/workflows/release\.yml@"'
}

pattern="$(identity_pattern_for kubefleet-dev/kubefleet)"
check_identity() { # check_identity <accept|reject> <san> <description>
  if grep -qE "${pattern}" <<<"$2"; then result=accept; else result=reject; fi
  if [ "${result}" = "$1" ]; then ok "$3"; else
    rc="n/a"; output="${result} for $2"; log=""; bad "$3"
  fi
}

check_identity accept \
  "https://github.com/kubefleet-dev/kubefleet/.github/workflows/release.yml@refs/tags/v0.4.0" \
  "accepts a tag-triggered release signature"
# A workflow_dispatch release signs under the branch ref, so the pattern must
# not pin refs/tags.
check_identity accept \
  "https://github.com/kubefleet-dev/kubefleet/.github/workflows/release.yml@refs/heads/main" \
  "accepts a dispatch-triggered release signature"
check_identity reject \
  "https://github.com/kubefleet-dev/kubefleet/.github/workflows/squad-release.yml@refs/tags/v0.4.0" \
  "rejects another workflow in the same repository"
check_identity reject \
  "https://github.com/kubefleet-dev/kubefleet/.github/workflows/release.yml.bak@refs/tags/v0.4.0" \
  "rejects a filename that merely starts with release.yml"
check_identity reject \
  "https://github.com/evil/kubefleet/.github/workflows/release.yml@refs/tags/v0.4.0" \
  "rejects the same workflow path in a different repository"
# Repository names may contain dots; unescaped they would match any character.
if [ "$(identity_pattern_for org/weird.name)" = '^https://github\.com/org/weird\.name/\.github/workflows/release\.yml@' ]; then
  ok "escapes dots in the repository name"
else
  rc="n/a"; output="$(identity_pattern_for org/weird.name)"; log=""; bad "escapes dots in the repository name"
fi

echo "== sign-crd-bundle.sh =="

pkg_dir="$(mktemp -d)"
printf 'abc123  kubefleet-crds-v0.4.0.tgz\n' >"${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256"
run_case TAG=v0.4.0 CRD_PACKAGE_DIR="${pkg_dir}" GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  bash "${sign_bundle_script}"
expect_rc 0 "checksum present: signs successfully"
expect_gh "cosign sign-blob --yes --bundle ${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256.bundle" \
  "writes the signature bundle next to the checksum"
expect_gh "cosign verify-blob" "verifies the blob signature it just created"
if [ -f "${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256.bundle" ]; then
  ok "signature bundle exists for upload"
else
  bad "signature bundle was not produced"
fi
rm -rf "${pkg_dir}"

pkg_dir="$(mktemp -d)"
run_case TAG=v0.4.0 CRD_PACKAGE_DIR="${pkg_dir}" GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  bash "${sign_bundle_script}"
expect_rc 1 "checksum missing: fails instead of signing nothing"
expect_no_gh "cosign" "checksum missing: does not invoke cosign at all"
rm -rf "${pkg_dir}"

pkg_dir="$(mktemp -d)"
printf 'abc123  kubefleet-crds-v0.4.0.tgz\n' >"${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256"
run_case TAG=v0.4.0 CRD_PACKAGE_DIR="${pkg_dir}" GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  FAKE_COSIGN_FAIL=verify-blob bash "${sign_bundle_script}"
expect_rc 1 "blob signature that will not verify: fails the release"
rm -rf "${pkg_dir}"

echo
echo "passed=${passed} failed=${failed}"
[ "${failed}" -eq 0 ]
