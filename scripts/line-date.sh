#!/usr/bin/env bash
# Prints the version line date for a release tag: the release date
# (YYYY-MM-DD, UTC) of its major.minor.0. A license covers a release when this
# date is on or before the license's updates_until, so later patches of a
# covered line stay covered.
#
#   scripts/line-date.sh v1.11.0   -> today (this is the x.y.0 release)
#   scripts/line-date.sh v1.11.3   -> the date tag v1.11.0 was made
#
# If the x.y.0 tag can't be found, it prints today and warns on stderr.
set -euo pipefail

TAG="${1:-}"
if [[ ! "$TAG" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "usage: $0 vX.Y.Z" >&2
  exit 2
fi
MAJOR="${BASH_REMATCH[1]}"
MINOR="${BASH_REMATCH[2]}"
PATCH="${BASH_REMATCH[3]}"
TODAY="$(date -u +%Y-%m-%d)"

if [[ "$PATCH" == "0" ]]; then
  echo "$TODAY"
  exit 0
fi

LINE_TAG="v${MAJOR}.${MINOR}.0"
# The tag's own date (annotated tags), else its commit's date, in UTC.
if DATE="$(TZ=UTC git for-each-ref --format='%(creatordate:format-local:%Y-%m-%d)' "refs/tags/${LINE_TAG}" 2>/dev/null)" && [[ -n "$DATE" ]]; then
  echo "$DATE"
  exit 0
fi

echo "[csm] warning: tag ${LINE_TAG} not found; using today (${TODAY}) as the line date" >&2
echo "$TODAY"
