#!/bin/sh -eu

f="doc/rest-api.yaml"

rest_api_hash() {
    md5sum "${f}" | cut -f1 -d" "
}

echo "Checking that ${f} is up to date..."

# make sure the YAML is up to date
hash1="$(rest_api_hash)"
mv "${f}" "${f}.bak"
make update-api -s
hash2="$(rest_api_hash)"
mv "${f}.bak" "${f}"
if [ "${hash1}" != "${hash2}" ]; then
    echo "==> Please update the ${f} file in your commit (make update-api)"
    exit 1
fi
