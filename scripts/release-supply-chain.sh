#!/usr/bin/env bash
set -euo pipefail

# Generate release supply-chain artifacts for binaries already present in ./dist.
#
# Produces:
#   - dist/SHA256SUMS.txt
#   - dist/moviedb.sbom.cdx.json
#   - dist/provenance.intoto.json
#   - cosign keyless signatures for binaries, checksum file, SBOM, and local provenance
#
# Notes:
#   - Real GitHub/SLSA provenance must be generated inside GitHub Actions with OIDC.
#     When this script runs in GitHub Actions and gh supports `gh attestation sign`,
#     set GITHUB_ATTEST=1 to publish GitHub artifact attestations.
#   - Local provenance generated here is useful release metadata, but it is not a
#     substitute for GitHub-hosted SLSA provenance.

DIST_DIR="${DIST_DIR:-dist}"
PROJECT_NAME="${PROJECT_NAME:-moviedb}"
SBOM_FILE="${SBOM_FILE:-$DIST_DIR/${PROJECT_NAME}.sbom.cdx.json}"
CHECKSUM_FILE="${CHECKSUM_FILE:-$DIST_DIR/SHA256SUMS.txt}"
PROVENANCE_FILE="${PROVENANCE_FILE:-$DIST_DIR/provenance.intoto.json}"
COSIGN_YES="${COSIGN_YES:-true}"
GITHUB_ATTEST="${GITHUB_ATTEST:-0}"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

optional_cmd() {
  command -v "$1" >/dev/null 2>&1
}

is_release_artifact() {
  local name
  name="$(basename "$1")"
  case "$name" in
    SHA256SUMS.txt|*.sha256|*.sig|*.pem|*.crt|*.bundle|*.intoto.json|*.intoto.jsonl|*.sbom.*|provenance.*)
      return 1
      ;;
  esac
  [[ -f "$1" ]]
}

artifact_sha256() {
  sha256sum "$1" | awk '{print $1}'
}

need_cmd awk
need_cmd date
need_cmd find
need_cmd git
need_cmd jq
need_cmd sha256sum

[[ -d "$DIST_DIR" ]] || die "distribution directory does not exist: $DIST_DIR"

mapfile -t artifacts < <(
  find "$DIST_DIR" -maxdepth 1 -type f -print |
    while IFS= read -r path; do
      if is_release_artifact "$path"; then
        printf '%s\n' "$path"
      fi
    done |
    sort
)

(( ${#artifacts[@]} > 0 )) || die "no release artifacts found in $DIST_DIR"

printf 'Release artifacts:\n'
printf '  %s\n' "${artifacts[@]}"

printf '\nGenerating checksums: %s\n' "$CHECKSUM_FILE"
{
  for artifact in "${artifacts[@]}"; do
    sha256sum "$artifact"
  done
} > "$CHECKSUM_FILE"

printf 'Generating CycloneDX SBOM: %s\n' "$SBOM_FILE"
if optional_cmd syft; then
  syft "dir:." -o cyclonedx-json="$SBOM_FILE"
elif optional_cmd cyclonedx-gomod; then
  cyclonedx-gomod app -licenses -json -output "$SBOM_FILE"
else
  die "install syft or cyclonedx-gomod to generate an SBOM"
fi

printf 'Generating local in-toto/SLSA-style provenance: %s\n' "$PROVENANCE_FILE"
commit="$(git rev-parse HEAD)"
repo_url="$(git config --get remote.origin.url || true)"
tag="$(git describe --tags --exact-match 2>/dev/null || true)"
dirty="false"
if ! git diff --quiet || ! git diff --cached --quiet; then
  dirty="true"
fi
started_on="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

subjects_json='[]'
for artifact in "${artifacts[@]}" "$CHECKSUM_FILE" "$SBOM_FILE"; do
  subjects_json="$(
    jq \
      --arg name "$(basename "$artifact")" \
      --arg sha "$(artifact_sha256 "$artifact")" \
      '. + [{name: $name, digest: {sha256: $sha}}]' \
      <<<"$subjects_json"
  )"
done

jq -n \
  --arg commit "$commit" \
  --arg repo "$repo_url" \
  --arg tag "$tag" \
  --arg started "$started_on" \
  --arg dirty "$dirty" \
  --arg buildType "https://github.com/randyabernethy/moviedb/release-script/v1" \
  --arg builderID "local:$(hostname 2>/dev/null || printf unknown)" \
  --arg goVersion "$(go version 2>/dev/null || true)" \
  --arg script "scripts/release-supply-chain.sh" \
  --argjson subjects "$subjects_json" \
  '{
    _type: "https://in-toto.io/Statement/v1",
    subject: $subjects,
    predicateType: "https://slsa.dev/provenance/v1",
    predicate: {
      buildDefinition: {
        buildType: $buildType,
        externalParameters: {
          repository: $repo,
          commit: $commit,
          tag: $tag,
          workingTreeDirty: ($dirty == "true"),
          script: $script
        }
      },
      runDetails: {
        builder: {
          id: $builderID,
          version: {
            go: $goVersion
          }
        },
        metadata: {
          startedOn: $started,
          finishedOn: (now | strftime("%Y-%m-%dT%H:%M:%SZ"))
        }
      }
    }
  }' > "$PROVENANCE_FILE"

printf 'Signing artifacts with cosign keyless signatures.\n'
need_cmd cosign
cosign_args=()
if [[ "$COSIGN_YES" == "true" ]]; then
  cosign_args+=(--yes)
fi

sign_targets=("${artifacts[@]}" "$CHECKSUM_FILE" "$SBOM_FILE" "$PROVENANCE_FILE")
for target in "${sign_targets[@]}"; do
  printf '  cosign sign-blob %s\n' "$target"
  cosign sign-blob "${cosign_args[@]}" \
    --output-signature "$target.sig" \
    --output-certificate "$target.pem" \
    "$target"
done

if [[ "$GITHUB_ATTEST" == "1" ]]; then
  need_cmd gh
  repo="${GITHUB_REPOSITORY:-}"
  if [[ -z "$repo" ]]; then
    repo="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)"
  fi
  [[ -n "$repo" ]] || die "GITHUB_ATTEST=1 requires GITHUB_REPOSITORY or a gh-authenticated repository"

  if ! gh attestation sign --help >/dev/null 2>&1; then
    die "gh attestation sign is not available in this GitHub CLI"
  fi

  printf 'Creating GitHub artifact attestations for binaries and release metadata.\n'
  for target in "${sign_targets[@]}"; do
    gh attestation sign "$target" --repo "$repo"
  done
fi

printf '\nDone. Release supply-chain outputs are in %s:\n' "$DIST_DIR"
printf '  %s\n' "$CHECKSUM_FILE" "$SBOM_FILE" "$PROVENANCE_FILE"
printf '  *.sig and *.pem for each signed artifact\n'
if [[ "$GITHUB_ATTEST" != "1" ]]; then
  printf '\nTip: run in GitHub Actions with GITHUB_ATTEST=1 for GitHub-hosted provenance/attestations.\n'
fi
