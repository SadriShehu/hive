#!/usr/bin/env bash
set -euo pipefail

last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
bump="${BUMP:-}"

if [ -n "$(git tag --points-at HEAD --list 'v[0-9]*')" ]; then
  echo "HEAD is already tagged, nothing to release"
  exit 0
fi

if [ -z "$bump" ] && [ -n "$last" ]; then
  code_changes=$(git diff --name-only "$last" HEAD | grep -vE '(^|/)[^/]+\.md$|^docs/|^LICENSE$' || true)
  if [ -z "$code_changes" ]; then
    echo "only documentation changed since $last, nothing to release"
    exit 0
  fi
fi

if [ -z "$bump" ]; then
  messages=$(git log "${last:+$last..}HEAD" --format=%B)
  case "$messages" in
    *'#major'*) bump=major ;;
    *'#minor'*) bump=minor ;;
    *) bump=patch ;;
  esac
fi

IFS=. read -r major minor patch <<<"${last#v}" || true
major=${major:-0}
minor=${minor:-0}
patch=${patch:-0}
case "$bump" in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
  *) echo "unknown bump: $bump" >&2; exit 1 ;;
esac

tag="v$major.$minor.$patch"
echo "next release: $tag ($bump bump after ${last:-no previous tag})"
echo "tag=$tag" >>"${GITHUB_OUTPUT:-/dev/null}"
