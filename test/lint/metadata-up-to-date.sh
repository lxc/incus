#!/bin/sh -eu

TEMPDIR=$(mktemp -d)

cleanup() {
  rm -Rf "${TEMPDIR}"
}
trap cleanup EXIT HUP INT TERM

hash_before="${TEMPDIR}/metadata-before.txt"
hash_after="${TEMPDIR}/metadata-after.txt"
json_metadata="internal/server/metadata/configuration.json"
doc_config_options="doc/config_options.txt"

metadata_hash() {
  files_to_check="${json_metadata} ${doc_config_options}"
  for f in $files_to_check; do
    if [ -f "$f" ]; then
      md5sum "$f"
    fi
  done | sort -k 2 > "$1"
}

echo "Checking that the metadata is up to date..."

# make sure the YAML metadata file and the documentation config option file are up to date
metadata_hash "$hash_before"
cp "${json_metadata}" "${json_metadata}.bak"
cp "${doc_config_options}" "${doc_config_options}.bak"
make update-metadata -s
metadata_hash "$hash_after"
mv "${json_metadata}.bak" "${json_metadata}"
mv "${doc_config_options}.bak" "${doc_config_options}"

d="$(diff -Nau "$hash_before" "$hash_after" || true)"
rm "$hash_before" "$hash_after"

if [ -z "$d" ]; then
    echo "==> metadata is up to date"
else
    echo "==> Please update the metadata in your commit (make update-metadata)"
    echo "==> Differences detected:"
    echo "$d"
    exit 1
fi
