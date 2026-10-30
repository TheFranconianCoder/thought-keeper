#!/usr/bin/env bash
# Publish a source-only release for an existing commit. Nothing is built and no assets are
# uploaded: GitHub's generated source archives are the artifacts. Never commits and never moves a
# tag.
# Usage: release-commit.sh X.Y.Z <commit> [--prerelease]
set -euo pipefail

fail() {
    printf 'release: %s\n' "$*" >&2
    exit 1
}

version="${1:-}"
ref="${2:-}"
flag="${3:-}"
if [ -z "$version" ] || [ -z "$ref" ]; then
    printf 'usage: release-commit.sh X.Y.Z <commit> [--prerelease]\n' >&2
    exit 2
fi
case "$flag" in
"" | --prerelease) ;;
*) fail "unknown flag '$flag' (expected --prerelease)" ;;
esac

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(git rev-parse --show-toplevel 2>/dev/null)" || fail "not inside a git repository"
cd "$root"

MISE=()
if command -v mise >/dev/null 2>&1; then
    MISE=(mise x --)
fi

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
    fail "version '$version' is not MAJOR.MINOR.PATCH"

tag="v$version"
commit="$(git rev-parse --verify "$ref^{commit}" 2>/dev/null)" || fail "'$ref' is not a commit"
git merge-base --is-ancestor "$commit" HEAD ||
    fail "'$ref' is not an ancestor of HEAD; only history on main is released"

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    fail "tag $tag already exists locally"
fi

title_file=".release/title-$version.txt"
notes_file=".release/notes-$version.md"
[ -s "$title_file" ] || fail "missing or empty $title_file"
[ -s "$notes_file" ] || fail "missing or empty $notes_file"

title="$(cat "$title_file")"
case "$title" in
*$'\n'*) fail "$title_file must be a single line" ;;
esac

"${MISE[@]}" gh auth status >/dev/null 2>&1 || fail "gh is not authenticated; run 'gh auth login'"
repo="$("${MISE[@]}" gh repo view --json nameWithOwner --jq .nameWithOwner)"

if "${MISE[@]}" gh api "repos/$repo/git/ref/tags/$tag" >/dev/null 2>&1; then
    fail "tag $tag already exists on GitHub"
fi
"${MISE[@]}" gh api "repos/$repo/commits/$commit" >/dev/null 2>&1 ||
    fail "commit $commit is not on GitHub; push main first"

args=(release create "$tag" --repo "$repo" --target "$commit" --title "$tag: $title" --notes-file "$notes_file")
if [ "$flag" = "--prerelease" ]; then
    args+=(--prerelease)
fi

printf '==> gh %s\n' "${args[*]}"
"${MISE[@]}" gh "${args[@]}"

# The tag is created on GitHub by `gh release create --target`; mirror it locally so
# `git describe` and the changelog range keep working without a reachable git remote.
git tag "$tag" "$commit"

url="$("${MISE[@]}" gh release view "$tag" --repo "$repo" --json url --jq .url)"
printf 'release %s published: %s\n' "$tag" "$url"
