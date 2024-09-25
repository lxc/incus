#!/bin/sh -eu
if ! command -v jq >/dev/null 2>&1; then
    echo "This tool requires: jq"
    exit 1
fi

## Delete anything that's tied to a project
for project in $(incus query "/1.0/projects?recursion=1" | jq .[].name -r); do
    echo "==> Deleting all instances for project: ${project}"
    for instance in $(incus query "/1.0/instances?recursion=1&project=${project}" | jq .[].name -r); do
        incus delete --project "${project}" -f "${instance}"
    done

    echo "==> Deleting all images for project: ${project}"
    for image in $(incus query "/1.0/images?recursion=1&project=${project}" | jq .[].fingerprint -r); do
        incus image delete --project "${project}" "${image}"
    done
done

for project in $(incus query "/1.0/projects?recursion=1" | jq .[].name -r); do
    echo "==> Deleting all profiles for project: ${project}"
    for profile in $(incus query "/1.0/profiles?recursion=1&project=${project}" | jq .[].name -r); do
        if [ "${profile}" = "default" ]; then
            printf 'config: {}\ndevices: {}' | incus profile edit --project "${project}" default
            continue
        fi
        incus profile delete --project "${project}" "${profile}"
    done

    if [ "${project}" != "default" ]; then
        echo "==> Deleting project: ${project}"
        incus project delete "${project}"
    fi
done

## Delete the networks
echo "==> Deleting all networks"
for network in $(incus query "/1.0/networks?recursion=1" | jq '.[] | select(.managed) | .name' -r); do
    incus network delete "${network}"
done

## Delete the storage pools
echo "==> Deleting all storage pools"
for storage_pool in $(incus query "/1.0/storage-pools?recursion=1" | jq .[].name -r); do
    # Delete the storage volumes.
    for volume in $(incus query "/1.0/storage-pools/${storage_pool}/volumes/custom?recursion=1" | jq .[].name -r); do
        echo "==> Deleting storage volume ${volume} on ${storage_pool}"
        incus storage volume delete "${storage_pool}" "${volume}"
    done

    # Delete the storage buckets.
    for bucket in $(incus query "/1.0/storage-pools/${storage_pool}/buckets?recursion=1" | jq .[].name -r); do
        echo "==> Deleting storage bucket ${bucket} on ${storage_pool}"
        incus storage volume delete "${storage_pool}" "${bucket}"
    done

    ## Delete the storage pool.
    incus storage delete "${storage_pool}"
done
