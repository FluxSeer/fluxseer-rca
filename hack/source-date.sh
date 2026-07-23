#!/usr/bin/env sh
set -eu

epoch="${1:-}"
if [ -z "$epoch" ]; then
  echo "missing SOURCE_DATE_EPOCH" >&2
  exit 1
fi

if date -u -r "$epoch" '+%Y-%m-%dT%H:%M:%SZ' >/dev/null 2>&1; then
  date -u -r "$epoch" '+%Y-%m-%dT%H:%M:%SZ'
  exit 0
fi

if date -u -d "@$epoch" '+%Y-%m-%dT%H:%M:%SZ' >/dev/null 2>&1; then
  date -u -d "@$epoch" '+%Y-%m-%dT%H:%M:%SZ'
  exit 0
fi

echo "date command does not support BSD -r or GNU -d epoch conversion" >&2
exit 1
