#!/usr/bin/env bash
set -euo pipefail

date_part="${1:-$(date -u +%m.%d)}"
if [[ ! "$date_part" =~ ^[0-9]{2}\.[0-9]{2}$ ]]; then
  echo "date must be in MM.DD format" >&2
  exit 1
fi

prefix="main-${date_part}"
max=0
while IFS= read -r tag; do
  [[ "$tag" == "$prefix" ]] && number=1 || number=0
  if [[ "$tag" == "$prefix".* ]]; then
    suffix="${tag#${prefix}.}"
    if [[ "$suffix" =~ ^[0-9]+$ ]]; then
      number="$((10#$suffix))"
    fi
  fi
  if (( number > max )); then
    max="$number"
  fi
done

if (( max == 0 )); then
  printf '%s\n' "$prefix"
else
  printf '%s.%d\n' "$prefix" "$((max + 1))"
fi
