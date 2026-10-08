#!/bin/bash
set -euo pipefail
[[ "$(id -u)" -eq 0 ]]
root=${1:?Usage: assemble-keys.sh CONFIG_DIRECTORY}
[[ "$root" = /* && -d "$root" && ! -L "$root" ]]
[[ "$(stat -c %u "$root")" -eq 0 ]]
root_mode=$(stat -c %a "$root")
[[ "$((8#$root_mode & 022))" -eq 0 ]]
shopt -s nullglob
fragments=("$root"/*.authorized_key)
[[ "${#fragments[@]}" -gt 0 ]]
for fragment in "$root/operator_keys" "${fragments[@]}"; do
  [[ -f "$fragment" && ! -L "$fragment" ]]
  [[ "$(stat -c %u "$fragment")" -eq 0 ]]
  mode=$(stat -c %a "$fragment")
  [[ "$((8#$mode & 022))" -eq 0 ]]
done
candidate=$(mktemp "$root/authorized_keys.next.XXXXXX")
umask 077
{
  cat "$root/operator_keys"
  printf '\n'
  for fragment in "${fragments[@]}"; do
    cat "$fragment"
    printf '\n'
  done
} > "$candidate"

fingerprints=$(ssh-keygen -lf "$candidate" | awk '{print $2}')
operator_fingerprints=$(ssh-keygen -lf "$root/operator_keys" | awk '{print $2}')
operator_count=$(printf '%s\n' "$operator_fingerprints" | wc -l)
operator_unique=$(printf '%s\n' "$operator_fingerprints" | sort -u | wc -l)
[[ "$(printf '%s\n' "$fingerprints" | wc -l)" -eq "$((operator_count + ${#fragments[@]}))" ]]
[[ "$(printf '%s\n' "$fingerprints" | sort -u | wc -l)" -eq "$((operator_unique + ${#fragments[@]}))" ]]
for fragment in "${fragments[@]}"; do
  [[ "$(ssh-keygen -lf "$fragment" | wc -l)" -eq 1 ]]
  [[ "$(grep -c '^restrict,command=' "$fragment")" -eq 1 ]]
  fingerprint=$(ssh-keygen -lf "$fragment" | awk '{print $2}')
  [[ "$(printf '%s\n' "$fingerprints" | grep -Fxc "$fingerprint")" -eq 1 ]]
done
[[ "$(grep -c '^restrict,command=' "$candidate")" -eq "${#fragments[@]}" ]]
chmod 0644 "$candidate"
mv "$candidate" "$root/authorized_keys"
printf 'PASS: preserved operator keys and %s distinct forced keys\n' "${#fragments[@]}"