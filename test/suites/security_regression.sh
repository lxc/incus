# shellcheck disable=SC2153 # INCUS_DIR/INCUS_UI are distinct harness env vars.
# Regression tests for fixed, publicly disclosed Incus security advisories.
#
# Each block is tagged with its GHSA and CVE and reproduces the original attack
# through the public API or client, then asserts the fixed behavior. A block
# must fail (the test aborts) if the vulnerability is reintroduced.
#
# VM, OVN, bridge-firewall and clustering advisories are covered here too. The
# suite runs in CI on a dedicated self-hosted runner that provides KVM, Open
# vSwitch and nftables, where every test is required, so a missing prerequisite
# fails the run there instead of being skipped.
#
# Three advisories need a custom malicious counterpart (a crafted migration peer
# or a hostile in-guest agent) that cannot be driven through the stock client or
# public API, so they are covered by Go unit tests instead:
#   - GHSA-q9fc-p923-6vp7 (dependent volumes via migration): the offer and response
#     header validation in internal/server/storage.
#   - GHSA-579w-c4rw-c8q3 (symlink traversal on migration receive): the nosymfollow
#     receive mount in internal/linux.
#   - GHSA-wfvq-qh87-gm4j (client file write via a malicious incus-agent): the
#     recursive pull symlink handling in cmd/incus.

# secreg_expect_fail MESSAGE CMD...
# Runs CMD, aborting the test if it succeeds, or if it fails without MESSAGE in
# its combined output.
secreg_expect_fail() {
    # shellcheck disable=SC2039,3043
    local msg out
    msg="${1}"
    shift

    if out="$("$@" 2>&1)"; then
        echo "FAIL: '$*' was expected to be rejected but succeeded" >&2
        return 1
    fi

    if ! printf '%s\n' "${out}" | grep -qF "${msg}"; then
        echo "FAIL: '$*' was rejected but without the expected message" >&2
        echo "  expected: ${msg}" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_incus ARGS...
# Invokes the incus binary directly, bypassing the eval-based test wrapper, so
# arguments containing quotes and spaces (JSON request bodies) survive verbatim.
# --data (not --data-file) is used by callers so the body is sent as JSON and
# the client still waits on any resulting operation.
secreg_incus() {
    # shellcheck disable=SC2039,3043
    local bin
    bin="$(unset -f incus incus_remote 2>/dev/null; command -v incus)"
    "${bin}" --force-local "$@"
}

# secreg_query_reject MESSAGE METHOD PATH JSON
# Sends JSON as an application/json request body and aborts unless the request
# is rejected with MESSAGE.
secreg_query_reject() {
    # shellcheck disable=SC2039,3043
    local msg method path json out
    msg="${1}"
    method="${2}"
    path="${3}"
    json="${4}"

    if out="$(secreg_incus query -X "${method}" --data "${json}" "${path}" 2>&1)"; then
        echo "FAIL: ${method} ${path} was expected to be rejected but succeeded" >&2
        return 1
    fi

    if ! printf '%s\n' "${out}" | grep -qF "${msg}"; then
        echo "FAIL: ${method} ${path} was rejected but without the expected message" >&2
        echo "  expected: ${msg}" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

test_security_regression_config_injection() {
    ensure_import_testimage

    incus init testimage c-secreg

    # The values carry a literal line break (JSON \n), sent straight to the API
    # so the server-side validator is exercised (the CLI would mangle newlines).

    # GHSA-x6jc-phwx-hp32 / CVE-2026-23953: newline injection in environment.*
    # values could add arbitrary lines (e.g. lxc.hook.pre-start) to the LXC config.
    secreg_query_reject "Environment variables cannot contain line breaks" PATCH /1.0/instances/c-secreg \
        '{"config":{"environment.FOO":"abc\nlxc.hook.pre-start=touch /root/secreg-pwned"}}'

    # GHSA-m3j6-p3v3-qmjv / CVE-2026-81498: same class through
    # nvidia.driver.capabilities (bypass of the environment.* fix).
    secreg_query_reject "NVIDIA configuration values cannot contain line breaks" PATCH /1.0/instances/c-secreg \
        '{"config":{"nvidia.driver.capabilities":"all\nlxc.hook.pre-start=touch /root/secreg-pwned"}}'

    # GHSA-7fj9-65v4-rp7h / CVE-2026-81494 (oci.dns.* half): newline injection
    # through oci.dns.* values.
    secreg_query_reject "Value cannot contain line breaks" PATCH /1.0/instances/c-secreg \
        '{"config":{"oci.dns.search":"example.com\nmalicious"}}'

    # GHSA-3wxw-5gh2-j2x2: OCI image config and oci.* keys (here oci.entrypoint)
    # were written into the line-based LXC config without validation, so a line
    # break could inject arbitrary directives such as a root pre-start hook.
    secreg_query_reject "OCI configuration values cannot contain line breaks" PATCH /1.0/instances/c-secreg \
        '{"config":{"oci.entrypoint":"/bin/sh\nlxc.hook.pre-start=touch /root/secreg-pwned"}}'

    # GHSA-v6mj-8pf4-hhw4 / CVE-2026-48755: argument injection in the backup
    # compression algorithm (only -1..-9 and --rsyncable are allowed arguments).
    secreg_query_reject "isn't allowed" POST /1.0/instances/c-secreg/backups \
        '{"name":"secreg-b","compression_algorithm":"zstd -d -f -o /root/secreg-pwned"}'

    [ ! -e /root/secreg-pwned ] || { echo "FAIL: a config/compression injection wrote /root/secreg-pwned" >&2; false; }

    incus delete -f c-secreg
}

test_security_regression_restricted_project() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local pool
    pool="$(incus storage list --format csv --columns n | head -n1)"

    incus project create secreg-proj \
        -c restricted=true \
        -c features.images=false

    # GHSA-q7xw-r4w2-2wcm / CVE-2026-62867: a restricted project could set the
    # root-run block.create_options filesystem option on a new volume.
    secreg_query_reject '"block.create_options" cannot be set in a restricted project' POST "/1.0/storage-pools/${pool}/volumes/custom?project=secreg-proj" \
        '{"name":"secreg-vol","type":"custom","content_type":"block","config":{"block.create_options":"-p /root/secreg-pwned"}}'

    # GHSA-4cph-ccqv-hm3c / CVE-2026-115372: the same restriction must also hold
    # when the option is introduced through a volume update (incomplete fix of 62867).
    secreg_incus query -X POST --data '{"name":"secreg-vol","type":"custom","content_type":"block"}' "/1.0/storage-pools/${pool}/volumes/custom?project=secreg-proj"
    secreg_query_reject '"block.create_options" cannot be set in a restricted project' PUT "/1.0/storage-pools/${pool}/volumes/custom/secreg-vol?project=secreg-proj" \
        '{"config":{"block.create_options":"-p /root/secreg-pwned"}}'
    incus storage volume delete "${pool}" secreg-vol --project secreg-proj

    # GHSA-53cg-qvg7-m8vg / CVE-2026-62313: a project restricted to isolated
    # containers must reject an instance that omits security.idmap.isolated.
    incus project set secreg-proj restricted.containers.privilege=isolated
    secreg_expect_fail 'security.idmap.isolated' \
        incus init testimage secreg-iso --project secreg-proj

    incus project delete secreg-proj

    [ ! -e /root/secreg-pwned ] || { echo "FAIL: a restricted-project bypass wrote /root/secreg-pwned" >&2; false; }
}

# secreg_import_reject MESSAGE TARFILE [IMPORT ARGS...]
# Imports an instance backup that must be rejected with MESSAGE.
secreg_import_reject() {
    # shellcheck disable=SC2039,3043
    local msg tarf out
    msg="${1}"
    tarf="${2}"
    shift 2

    if out="$(secreg_incus import "${tarf}" "$@" 2>&1)"; then
        echo "FAIL: import of ${tarf} was expected to be rejected but succeeded" >&2
        return 1
    fi

    if ! printf '%s\n' "${out}" | grep -qF "${msg}"; then
        echo "FAIL: import of ${tarf} was rejected but without the expected message" >&2
        echo "  expected: ${msg}" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_assert_alive
# Confirms the daemon is still responsive (i.e. a crafted input did not crash it).
secreg_assert_alive() {
    if ! secreg_incus info >/dev/null 2>&1; then
        echo "FAIL: the daemon became unresponsive" >&2
        return 1
    fi
}

# secreg_import_fails_safely TARFILE [IMPORT ARGS...]
# Imports a malformed backup that must be rejected gracefully, without a panic.
secreg_import_fails_safely() {
    # shellcheck disable=SC2039,3043
    local tarf out
    tarf="${1}"
    shift

    if out="$(secreg_incus import "${tarf}" "$@" 2>&1)"; then
        echo "FAIL: import of ${tarf} was expected to be rejected but succeeded" >&2
        return 1
    fi

    if printf '%s\n' "${out}" | grep -qiE 'panic|runtime error|invalid memory address|segmentation'; then
        echo "FAIL: import of ${tarf} triggered a panic" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

test_security_regression_backup_import() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local pool work d out
    pool="$(incus storage list --format csv --columns n | head -n1)"
    work="${INCUS_DIR}/secreg-backup"
    rm -rf "${work}"
    mkdir -p "${work}"

    # A clean, uncompressed instance backup used as the base to tamper with.
    incus init testimage secreg-src
    incus export secreg-src "${work}/base.tar" --instance-only --compression none
    incus delete -f secreg-src

    # GHSA-26gp-p5fw-3r2h / CVE-2026-81496: a path-traversal instance name in
    # backup/index.yaml escaped the storage pool on import.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/base.tar" -C "${d}"
    sed -i 's|^name: secreg-src$|name: ../../../../../../root/secreg-pwned|' "${d}/backup/index.yaml"
    tar cf "${work}/evil-name.tar" -C "${d}" backup
    rm -rf "${d}"
    # instance.ValidName now rejects the traversal name (the "/" makes it an
    # invalid, non-snapshot instance name) before any pool directory is created.
    secreg_import_reject "reserved for snapshots" "${work}/evil-name.tar"
    secreg_assert_alive

    # GHSA-67qw-68v3-36h6 / CVE-2026-81495: a path-traversal volume name in the
    # X-Incus-name header of a custom volume import escaped the storage pool.
    # The octet-stream import path keys on Content-Type, so send it raw.
    incus storage volume create "${pool}" secreg-volsrc
    incus storage volume export "${pool}" secreg-volsrc "${work}/base-vol.tar" --compression none
    incus storage volume delete "${pool}" secreg-volsrc
    out="$(curl -s --unix-socket "${INCUS_DIR}/unix.socket" -X POST \
        -H 'X-Incus-name: ../../../../../../root/secreg-pwned' \
        -H 'Content-Type: application/octet-stream' \
        --data-binary "@${work}/base-vol.tar" \
        "http://localhost/1.0/storage-pools/${pool}/volumes/custom")"
    if ! printf '%s\n' "${out}" | grep -qF 'Name contains invalid character'; then
        echo "FAIL: custom volume import accepted a path-traversal X-Incus-name" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi
    secreg_assert_alive

    # GHSA-67wx-r9xr-x75x / CVE-2026-41648: the backup index metadata was decoded
    # without a size limit, allowing a memory-exhaustion DoS. The decode is now
    # bounded (1 MiB), so an oversized index.yaml is rejected rather than consumed.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/base.tar" -C "${d}"
    {
        printf '\n# '
        head -c 1500000 /dev/zero | tr '\0' 'a'
        printf '\n'
    } >> "${d}/backup/index.yaml"
    tar cf "${work}/evil-huge.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_import_fails_safely "${work}/evil-huge.tar"
    secreg_assert_alive

    rm -rf "${work}"
    [ ! -e /root/secreg-pwned ] || { echo "FAIL: a backup import wrote /root/secreg-pwned" >&2; false; }
}

# secreg_trust_restricted NAME PROJECTS
# Generates a client certificate and trusts it, restricted to PROJECTS.
secreg_trust_restricted() {
    gen_cert_and_key "${INCUS_CONF}/${1}.key" "${INCUS_CONF}/${1}.crt" "${1}.local"
    incus config trust add-certificate "${INCUS_CONF}/${1}.crt" --restricted --projects "${2}"
}

# secreg_cert_forbidden NAME PATH [CURL ARGS...]
# Issues an authenticated request with the NAME certificate and aborts unless
# the daemon denies it (HTTP 403).
secreg_cert_forbidden() {
    # shellcheck disable=SC2039,3043
    local cert path out
    cert="${1}"
    path="${2}"
    shift 2

    out="$(curl -k -s --cert "${INCUS_CONF}/${cert}.crt" --key "${INCUS_CONF}/${cert}.key" "$@" "https://${INCUS_ADDR}${path}")"
    if ! printf '%s\n' "${out}" | grep -q '"error_code":403'; then
        echo "FAIL: ${path} was expected to be forbidden for a restricted certificate" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_cert_allowed NAME PATH [CURL ARGS...]
# Issues an authenticated request with the NAME certificate and aborts if the
# daemon denies it (used to prove a restricted cert does hold the weaker access,
# so a sibling denial is the specific fix and not a blanket refusal).
secreg_cert_allowed() {
    # shellcheck disable=SC2039,3043
    local cert path out
    cert="${1}"
    path="${2}"
    shift 2

    out="$(curl -k -s --cert "${INCUS_CONF}/${cert}.crt" --key "${INCUS_CONF}/${cert}.key" "$@" "https://${INCUS_ADDR}${path}")"
    if printf '%s\n' "${out}" | grep -q '"error_code":403'; then
        echo "FAIL: ${path} was expected to be allowed for the restricted certificate" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

test_security_regression_project_isolation() {
    ensure_import_testimage
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # shellcheck disable=SC2039,3043
    local pool opid opcreate
    pool="$(incus storage list --format csv --columns n | head -n1)"

    # An empty project the "outsider" certificate is confined to, and a secret
    # project (sharing default's images/profiles for a usable root disk) holding
    # resources it must not be able to reach.
    incus project create secreg-cp -c features.profiles=false
    incus project create secreg-secret -c features.images=false -c features.profiles=false
    incus network address-set create secreg-aset --project default
    incus storage volume create "${pool}" secreg-secretvol --project secreg-secret
    incus launch testimage secreg-secretinst --project secreg-secret

    # Outsider certificate: can only see the empty secreg-cp project.
    secreg_trust_restricted secreg-out secreg-cp
    # Default-scoped certificate: used for cross-project copies into default.
    secreg_trust_restricted secreg-def default

    # GHSA-6v6x-387m-rj4w / CVE-2026-81499: the network address-set API was
    # missing from the authorizer's project-expansion switch, so a restricted
    # client could reach another project's address sets.
    secreg_cert_forbidden secreg-out "/1.0/network-address-sets/secreg-aset?project=default"

    # GHSA-64f3-v33m-w89f / CVE-2026-55621: copying a custom volume from a
    # project the client cannot view skipped the source permission check.
    secreg_cert_forbidden secreg-def "/1.0/storage-pools/${pool}/volumes/custom?project=default" \
        -X POST -d "{\"name\":\"secreg-stolenvol\",\"type\":\"custom\",\"source\":{\"type\":\"copy\",\"pool\":\"${pool}\",\"name\":\"secreg-secretvol\",\"project\":\"secreg-secret\"}}"

    # GHSA-jh4v-j34r-2mgh / CVE-2026-115376: same copy bypass when source.type is
    # omitted (the official client always sets it, so the empty-type arm was
    # skipping the check).
    secreg_cert_forbidden secreg-def "/1.0/storage-pools/${pool}/volumes/custom?project=default" \
        -X POST -d "{\"name\":\"secreg-stolenvol\",\"type\":\"custom\",\"source\":{\"pool\":\"${pool}\",\"name\":\"secreg-secretvol\",\"project\":\"secreg-secret\"}}"

    # GHSA-c9f5-j9c3-mhrg / CVE-2026-55622: copying an instance from a project
    # the client cannot view skipped the source permission check.
    secreg_cert_forbidden secreg-def "/1.0/instances?project=default" \
        -X POST -d '{"name":"secreg-stoleninst","source":{"type":"copy","source":"secreg-secretinst","project":"secreg-secret"}}'

    # GHSA-mfwv-x733-9446 / CVE-2026-115373: GET on an operation did not check
    # project access, so a restricted client could read another project's
    # operation (command line, environment, secrets).
    # A wait-for-websocket exec operation stays Running with no client attached,
    # so the operation id is stable without a flaky background exec channel.
    opcreate="$(curl -k -s --unix-socket "${INCUS_DIR}/unix.socket" -X POST \
        -d '{"command":["/bin/sleep","300"],"wait-for-websocket":true,"interactive":false,"environment":{}}' \
        "incus/1.0/instances/secreg-secretinst/exec?project=secreg-secret")"
    opid="$(printf '%s\n' "${opcreate}" | jq -r '.metadata.id // empty')"
    if [ -z "${opid}" ]; then
        echo "FAIL: could not create a running operation for the GHSA-mfwv test" >&2
        printf '  got: %s\n' "${opcreate}" >&2
        false
        return
    fi
    secreg_cert_forbidden secreg-out "/1.0/operations/${opid}"

    # GHSA-5c64-j5m6-8433, GHSA-jcw9-v2ff-8m79: creation entitlements were checked
    # against the requested project while the resource was created in the project it
    # is inherited from, so a client restricted to a feature-disabled project could
    # create resources in the default project. secreg-cp disables the profiles
    # feature, so a profile it "creates" is inherited into the default project and
    # must be refused rather than authorized against secreg-cp.
    secreg_cert_forbidden secreg-out "/1.0/profiles?project=secreg-cp" \
        -X POST -d '{"name":"secreg-evilprof"}'

    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-out.crt")"
    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-def.crt")"
    incus delete -f secreg-secretinst --project secreg-secret
    incus storage volume delete "${pool}" secreg-secretvol --project secreg-secret
    incus network address-set delete secreg-aset --project default
    incus project delete secreg-secret
    incus project delete secreg-cp
    rm -f "${INCUS_CONF}/secreg-out.crt" "${INCUS_CONF}/secreg-out.key" "${INCUS_CONF}/secreg-def.crt" "${INCUS_CONF}/secreg-def.key"
}

test_security_regression_storage_pool_perms() {
    # GHSA-56mx-8g9f-5crf / CVE-2025-64507: storage pool directories were world
    # traversable, letting an unprivileged incus-group user reach a shifted
    # custom volume and plant a setuid binary. Pool directories must not be
    # accessible to other users.
    incus storage create secreg-pool dir
    # shellcheck disable=SC2039,3043
    local src mode other
    src="$(incus storage get secreg-pool source)"

    for dir in "${src}" "${src}/containers" "${src}/custom" "${src}/images" "${src}/buckets"; do
        [ -d "${dir}" ] || continue
        mode="$(stat -c '%a' "${dir}")"
        other="$(printf '%s' "${mode}" | tail -c1)"
        case "${other}" in
            0|1) ;;
            *)
                echo "FAIL: ${dir} is accessible to other users (mode ${mode})" >&2
                false
                ;;
        esac
    done

    incus storage delete secreg-pool
}

# secreg_import_no_panic TARFILE [IMPORT ARGS...]
# Imports a crafted backup that must not crash the daemon. The import may fail
# or succeed, but a panic (the original DoS) is a failure.
secreg_import_no_panic() {
    # shellcheck disable=SC2039,3043
    local tarf out
    tarf="${1}"
    shift

    out="$(secreg_incus import "${tarf}" "$@" 2>&1 || true)"
    if printf '%s\n' "${out}" | grep -qiE 'panic|runtime error|invalid memory address|segmentation|slice bounds out of range'; then
        echo "FAIL: import of ${tarf} triggered a panic" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_volimport_reject MESSAGE POOL TARFILE
# Imports a custom volume backup that must be rejected with MESSAGE.
secreg_volimport_reject() {
    # shellcheck disable=SC2039,3043
    local msg pool tarf out
    msg="${1}"
    pool="${2}"
    tarf="${3}"

    if out="$(secreg_incus storage volume import "${pool}" "${tarf}" 2>&1)"; then
        echo "FAIL: volume import of ${tarf} was expected to be rejected but succeeded" >&2
        return 1
    fi

    if ! printf '%s\n' "${out}" | grep -qF "${msg}"; then
        echo "FAIL: volume import of ${tarf} rejected without the expected message" >&2
        echo "  expected: ${msg}" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_volimport_no_panic POOL TARFILE
# Imports a crafted custom volume backup that must not crash the daemon.
secreg_volimport_no_panic() {
    # shellcheck disable=SC2039,3043
    local pool tarf out
    pool="${1}"
    tarf="${2}"

    out="$(secreg_incus storage volume import "${pool}" "${tarf}" 2>&1 || true)"
    if printf '%s\n' "${out}" | grep -qiE 'panic|runtime error|invalid memory address|segmentation|slice bounds out of range'; then
        echo "FAIL: volume import of ${tarf} triggered a panic" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_yaml_drop_block FILE KEY
# Removes a two-space-indented block ("  KEY:") and its nested lines from a
# backup index.yaml, up to the next two-space-indented key.
secreg_yaml_drop_block() {
    # shellcheck disable=SC2039,3043
    local file key tmp
    file="${1}"
    key="${2}"
    tmp="${file}.tmp"

    awk -v key="${key}" '
        $0 ~ "^  " key ":[[:space:]]*$" { skip=1; next }
        skip && /^  [A-Za-z_]+:/ { skip=0 }
        !skip { print }
    ' "${file}" > "${tmp}"
    mv "${tmp}" "${file}"
}

test_security_regression_backup_dos() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local pool work d
    pool="$(incus storage list --format csv --columns n | head -n1)"
    work="${INCUS_DIR}/secreg-dos"
    rm -rf "${work}"
    mkdir -p "${work}"

    # Base instance backups (with and without a snapshot) to tamper with.
    incus init testimage secreg-src
    incus export secreg-src "${work}/inst.tar" --instance-only --compression none
    incus snapshot create secreg-src snap0
    incus export secreg-src "${work}/inst-snap.tar" --compression none
    incus delete -f secreg-src

    # Base custom volume backup with a snapshot.
    incus storage volume create "${pool}" secreg-volsrc
    incus storage volume snapshot create "${pool}" secreg-volsrc vsnap0
    incus storage volume export "${pool}" secreg-volsrc "${work}/vol.tar" --compression none
    incus storage volume delete "${pool}" secreg-volsrc

    # GHSA-hpjh-q53p-f27r / CVE-2026-115375: a dependent custom volume in an
    # instance backup whose name is a traversal chain escaped the pool on import
    # (bypass of the 81495/81496 name checks).
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/inst.tar" -C "${d}"
    cat >> "${d}/backup/index.yaml" <<EOF
  dependent_volumes:
    - volume:
        name: ../../../../../../root/secreg-pwned
        type: custom
        config: {}
      pool:
        name: ${pool}
        driver: dir
EOF
    tar cf "${work}/hpjh.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_import_reject "Invalid dependent volume name" "${work}/hpjh.tar"
    secreg_assert_alive

    # GHSA-4xg6-52mh-fpw8 / CVE-2026-48754: a null dependent volume entry crashed
    # the import with a nil dereference.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/inst.tar" -C "${d}"
    printf '  dependent_volumes:\n    - null\n' >> "${d}/backup/index.yaml"
    tar cf "${work}/4xg6.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_import_reject "Bad dependent volume definition found in index" "${work}/4xg6.tar"
    secreg_assert_alive

    # GHSA-8g7m-96c8-8wwc / CVE-2026-47753: an instance backup index without a
    # volume block dereferenced Config.Volume.Config.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/inst.tar" -C "${d}"
    secreg_yaml_drop_block "${d}/backup/index.yaml" volume
    tar cf "${work}/8g7m.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_import_no_panic "${work}/8g7m.tar"
    secreg_assert_alive
    incus delete -f secreg-src 2>/dev/null || true

    # GHSA-4m88-wxj4-9qj6 / CVE-2026-40251: a physical snapshot with empty snapshot
    # metadata caused an out-of-bounds slice panic during restore.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/inst-snap.tar" -C "${d}"
    secreg_yaml_drop_block "${d}/backup/index.yaml" snapshots
    secreg_yaml_drop_block "${d}/backup/index.yaml" volume_snapshots
    tar cf "${work}/4m88.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_import_no_panic "${work}/4m88.tar"
    secreg_assert_alive
    incus delete -f secreg-src 2>/dev/null || true

    # GHSA-r7w7-mmxr-47r9 / CVE-2026-40197: a null entry in volume_snapshots
    # crashed a custom volume import with a nil dereference.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/vol.tar" -C "${d}"
    secreg_yaml_drop_block "${d}/backup/index.yaml" volume_snapshots
    printf '  volume_snapshots:\n    - null\n' >> "${d}/backup/index.yaml"
    tar cf "${work}/r7w7.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_volimport_reject "Bad snapshot definition found in index" "${pool}" "${work}/r7w7.tar"
    secreg_assert_alive

    # GHSA-xhqx-mgh3-3h7q / CVE-2026-48756: a volume snapshot lacking expires_at
    # dereferenced a nil *time.Time during import.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/vol.tar" -C "${d}"
    sed -i '/^      expires_at:/d' "${d}/backup/index.yaml"
    tar cf "${work}/xhqx.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_volimport_no_panic "${pool}" "${work}/xhqx.tar"
    secreg_assert_alive
    incus storage volume delete "${pool}" secreg-volsrc 2>/dev/null || true

    # GHSA-x6pw-mh5w-pq73: a null entry in an instance backup's snapshot list
    # dereferenced a nil snapshot while rewriting backup.yaml on import. The config
    # that gets rewritten is the on-disk backup/container/backup.yaml, where the
    # snapshots list is top-level, so the null is injected there.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/inst-snap.tar" -C "${d}"
    awk '
        /^snapshots:/ { print "snapshots:"; print "- null"; skip=1; next }
        skip && /^[A-Za-z]/ { skip=0 }
        !skip { print }
    ' "${d}/backup/container/backup.yaml" > "${d}/backup/container/backup.yaml.tmp"
    mv "${d}/backup/container/backup.yaml.tmp" "${d}/backup/container/backup.yaml"
    tar cf "${work}/x6pw.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_import_reject "Bad snapshot definition found in index" "${work}/x6pw.tar"
    secreg_assert_alive
    incus delete -f secreg-src 2>/dev/null || true

    # GHSA-p45f-gqqj-6qq3: the snapshot names in the volume config section of a
    # backup index were used as path segments without validation. They must now be
    # valid names and match the validated top-level snapshot list; a config entry
    # that doesn't match (here a traversal chain) is rejected.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/vol.tar" -C "${d}"
    sed -i 's#name: vsnap0#name: ../../../../../../root/secreg-pwned#' "${d}/backup/index.yaml"
    tar cf "${work}/p45f.tar" -C "${d}" backup
    rm -rf "${d}"
    secreg_volimport_reject "Invalid snapshot name" "${pool}" "${work}/p45f.tar"
    secreg_assert_alive
    incus storage volume delete "${pool}" secreg-volsrc 2>/dev/null || true

    rm -rf "${work}"
    [ ! -e /root/secreg-pwned ] || { echo "FAIL: a backup import DoS case wrote /root/secreg-pwned" >&2; false; }
}

# secreg_bktimport_reject MESSAGE POOL TARFILE
# Imports a storage bucket backup that must be rejected with MESSAGE.
secreg_bktimport_reject() {
    # shellcheck disable=SC2039,3043
    local msg pool tarf out
    msg="${1}"
    pool="${2}"
    tarf="${3}"

    if out="$(secreg_incus storage bucket import "${pool}" "${tarf}" 2>&1)"; then
        echo "FAIL: bucket import of ${tarf} was expected to be rejected but succeeded" >&2
        return 1
    fi

    if ! printf '%s\n' "${out}" | grep -qF "${msg}"; then
        echo "FAIL: bucket import of ${tarf} rejected without the expected message" >&2
        echo "  expected: ${msg}" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

# secreg_bktimport_no_panic POOL TARFILE
# Imports a crafted storage bucket backup that must not crash the daemon.
secreg_bktimport_no_panic() {
    # shellcheck disable=SC2039,3043
    local pool tarf out
    pool="${1}"
    tarf="${2}"

    out="$(secreg_incus storage bucket import "${pool}" "${tarf}" 2>&1 || true)"
    if printf '%s\n' "${out}" | grep -qiE 'panic|runtime error|invalid memory address|segmentation|slice bounds out of range'; then
        echo "FAIL: bucket import of ${tarf} triggered a panic" >&2
        printf '  got: %s\n' "${out}" >&2
        return 1
    fi
}

test_security_regression_image_symlink() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local work ex f out
    work="${INCUS_DIR}/secreg-img"
    rm -rf "${work}"
    mkdir -p "${work}"
    rm -f /tmp/secreg-pwned

    # Export the real test image to use as a valid container base to tamper.
    incus image export testimage "${work}/base"
    f="$(find "${work}" -maxdepth 1 -name 'base*' | head -1)"
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"

    # GHSA-6rqx-22hc-qm36 / CVE-2026-63125: an image shipping backup.yaml as a
    # symlink made the daemon write the instance config through it at create
    # (e.g. backup.yaml -> /etc/ld.so.preload for host code execution).
    # incus init reads stdin for a preseed, so it is fed /dev/null to avoid a hang.
    ln -sf /tmp/secreg-pwned "${ex}/backup.yaml"
    tar czf "${work}/evil-backup.tar.gz" -C "${ex}" .
    rm -rf "${ex}"
    incus image import "${work}/evil-backup.tar.gz" --alias secreg-img-backup
    out="$(incus init secreg-img-backup secreg-imginst < /dev/null 2>&1 || true)"
    incus delete -f secreg-imginst 2>/dev/null || true
    incus image delete secreg-img-backup
    if ! printf '%s\n' "${out}" | grep -qF "disallowed entries"; then
        echo "FAIL: image backup.yaml symlink was not rejected at instance create" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi
    secreg_assert_alive

    # GHSA-vxp5-584q-c479 / CVE-2026-48752: templates shipped as a symlink in an
    # image let the daemon write rendered templates through it as root.
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    rm -rf "${ex}/templates"
    ln -s /tmp/secreg-pwned "${ex}/templates"
    tar czf "${work}/evil-templates.tar.gz" -C "${ex}" .
    rm -rf "${ex}"
    incus image import "${work}/evil-templates.tar.gz" --alias secreg-img-tpl
    out="$(incus init secreg-img-tpl secreg-imgtplinst < /dev/null 2>&1 || true)"
    incus delete -f secreg-imgtplinst 2>/dev/null || true
    incus image delete secreg-img-tpl
    if ! printf '%s\n' "${out}" | grep -qF "disallowed entries"; then
        echo "FAIL: image templates symlink was not rejected at instance create" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi
    secreg_assert_alive

    # GHSA-73hr-m85f-64v9 / CVE-2026-48750: an exec-output symlink in an image let
    # a recorded exec write its output through the symlink as root.
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    ln -s /tmp/secreg-pwned "${ex}/exec-output"
    tar czf "${work}/evil-execout.tar.gz" -C "${ex}" .
    rm -rf "${ex}"
    incus image import "${work}/evil-execout.tar.gz" --alias secreg-img-execout
    out="$(incus init secreg-img-execout secreg-imgexecinst < /dev/null 2>&1 || true)"
    incus delete -f secreg-imgexecinst 2>/dev/null || true
    incus image delete secreg-img-execout
    if ! printf '%s\n' "${out}" | grep -qF "disallowed entries"; then
        echo "FAIL: image exec-output symlink was not rejected at instance create" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi
    secreg_assert_alive

    # GHSA-5pgx-q9xx-j3qv: the metadata strip was extended from symlinks to all
    # non-regular files, so a special file (here a FIFO) shipped in image metadata
    # is now rejected too, not just symlinks.
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    mkfifo "${ex}/secreg-fifo"
    tar czf "${work}/evil-fifo.tar.gz" -C "${ex}" .
    rm -rf "${ex}"
    incus image import "${work}/evil-fifo.tar.gz" --alias secreg-img-fifo
    out="$(incus init secreg-img-fifo secreg-imgfifoinst < /dev/null 2>&1 || true)"
    incus delete -f secreg-imgfifoinst 2>/dev/null || true
    incus image delete secreg-img-fifo
    if ! printf '%s\n' "${out}" | grep -qF "disallowed entries"; then
        echo "FAIL: a special file in image metadata was not rejected at instance create" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi
    secreg_assert_alive

    # GHSA-2q3f-q5pq-g8wv / CVE-2026-48749: a rootfs symlink in an image redirected
    # the chroot file API to the host. The image carries a real rootfs/ (so it is
    # typed as a container) plus a trailing rootfs symlink; unpacking must refuse to
    # turn rootfs into a symlink and the create must fail with nothing written.
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    tar cf "${work}/evil-rootfs.tar" -C "${ex}" .
    rm -rf "${ex}"
    sl="$(mktemp -d "${work}/XXXXXX")"
    ln -s / "${sl}/rootfs"
    tar rf "${work}/evil-rootfs.tar" -C "${sl}" rootfs
    rm -rf "${sl}"
    gzip -f "${work}/evil-rootfs.tar"
    incus image import "${work}/evil-rootfs.tar.gz" --alias secreg-img-rootfs
    if incus init secreg-img-rootfs secreg-imgrootfsinst < /dev/null >/dev/null 2>&1; then
        echo "FAIL: image rootfs symlink was accepted at instance create" >&2
        incus delete -f secreg-imgrootfsinst 2>/dev/null || true
        incus image delete secreg-img-rootfs 2>/dev/null || true
        false
    fi
    incus delete -f secreg-imgrootfsinst 2>/dev/null || true
    incus image delete secreg-img-rootfs
    secreg_assert_alive

    rm -rf "${work}"
    [ ! -e /tmp/secreg-pwned ] || { echo "FAIL: an image symlink wrote /tmp/secreg-pwned" >&2; rm -f /tmp/secreg-pwned; false; }
}

test_security_regression_bucket_dos() {
    # shellcheck disable=SC2039,3043
    local incus_backend poolName buckets_addr loop_file_1 loop_device_1 mnt work d isz keep

    incus_backend=$(storage_backend "$INCUS_DIR")
    poolName=$(incus profile device get default root pool)
    work="${INCUS_DIR}/secreg-bktdos"
    rm -rf "${work}"
    mkdir -p "${work}"
    mnt=""

    # Buckets on a dir pool need a real filesystem (the test suite may run on tmpfs).
    if [ "${incus_backend}" = "dir" ]; then
        configure_loop_device loop_file_1 loop_device_1
        mkfs.ext4 "${loop_device_1}"
        mnt="${TEST_DIR}/secreg-bktdos-mnt"
        mkdir "${mnt}"
        mount "${loop_device_1}" "${mnt}"
        losetup -d "${loop_device_1}"
        mkdir "${mnt}/s3"
        incus storage create secreg-s3 dir source="${mnt}/s3"
        poolName="secreg-s3"
    fi

    buckets_addr="127.0.0.1:$(local_tcp_port)"
    incus config set core.storage_buckets_address "${buckets_addr}"

    # GHSA-gc7j-g665-rxr9 / CVE-2026-40195: a bucket backup index.yaml without a
    # config block dereferenced a nil Config.Bucket.
    d="$(mktemp -d "${work}/XXXXXX")"
    mkdir -p "${d}/backup"
    cat > "${d}/backup/index.yaml" <<EOF
name: secreg-bkt
backend: dir
pool: ${poolName}
type: bucket
EOF
    tar czf "${work}/gc7j.tar.gz" -C "${d}" backup
    rm -rf "${d}"
    secreg_bktimport_reject "Valid bucket config not found in index" "${poolName}" "${work}/gc7j.tar.gz"
    secreg_assert_alive

    # GHSA-vg76-xmhg-j5x3 / CVE-2026-33743: a non-index tar entry shorter than
    # "backup/bucket/" caused a slice-bounds panic in UploadAllFiles.
    d="$(mktemp -d "${work}/XXXXXX")"
    mkdir -p "${d}/backup"
    cat > "${d}/backup/index.yaml" <<EOF
name: secreg-bkt
backend: dir
pool: ${poolName}
type: bucket
config:
  bucket:
    name: secreg-bkt
    description: ""
    config: {}
EOF
    echo x > "${d}/backup/b"
    tar czf "${work}/vg76.tar.gz" -C "${d}" backup
    rm -rf "${d}"
    secreg_bktimport_no_panic "${poolName}" "${work}/vg76.tar.gz"
    secreg_assert_alive
    incus storage bucket delete "${poolName}" secreg-bkt 2>/dev/null || true

    # GHSA-fwj8-62r8-8p8m / CVE-2026-41647: a truncated bucket backup made tr.Next()
    # return a non-EOF error with a nil header, dereferenced in UploadAllFiles. The
    # index.yaml sets optimized so GetInfo stops reading right after it and the
    # truncation is only reached later while streaming the bucket objects.
    d="$(mktemp -d "${work}/XXXXXX")"
    mkdir -p "${d}/backup/bucket"
    cat > "${d}/backup/index.yaml" <<EOF
name: secreg-bkt
backend: dir
pool: ${poolName}
type: bucket
optimized: false
config:
  bucket:
    name: secreg-bkt
    description: ""
    config: {}
  bucket_keys:
    - name: admin
      description: ""
      role: admin
EOF
    head -c 8192 /dev/zero | tr '\0' A > "${d}/backup/bucket/data"
    tar cf "${work}/fwj8.tar" -C "${d}" backup/index.yaml backup/bucket/data
    isz="$(wc -c < "${d}/backup/index.yaml")"
    # Keep the full index entry plus a partial second header so tr.Next() errors.
    keep=$(( 512 + ((isz + 511) / 512) * 512 + 256 ))
    truncate -s "${keep}" "${work}/fwj8.tar"
    rm -rf "${d}"
    secreg_bktimport_no_panic "${poolName}" "${work}/fwj8.tar"
    secreg_assert_alive
    incus storage bucket delete "${poolName}" secreg-bkt 2>/dev/null || true

    incus config unset core.storage_buckets_address
    if [ "${incus_backend}" = "dir" ]; then
        incus storage delete secreg-s3
        [ -z "${mnt}" ] || umount "${mnt}" 2>/dev/null || true
        deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    fi
    rm -rf "${work}"
}

# secreg_tpl_metadata FILE ARCH TARGET SOURCE WHEN
# Writes an image metadata.yaml carrying a single template entry.
secreg_tpl_metadata() {
    # shellcheck disable=SC2039,3043
    local file arch target source when
    file="${1}"
    arch="${2}"
    target="${3}"
    source="${4}"
    when="${5}"
    cat > "${file}" <<EOF
architecture: ${arch}
creation_date: 1793289598
properties:
  description: secreg-template
  os: BusyBox
templates:
  "${target}":
    when:
      - ${when}
    template: ${source}
EOF
}

# secreg_tpl_rejected INST SNIP
# Aborts unless the named instance's start hook failed with SNIP in the log.
secreg_tpl_rejected() {
    # shellcheck disable=SC2039,3043
    local inst snip
    inst="${1}"
    snip="${2}"
    if ! grep -F "instance=${inst}" "${INCUS_DIR}/incus.log" 2>/dev/null | grep -qF "${snip}"; then
        echo "FAIL: ${inst} template start was not rejected with: ${snip}" >&2
        grep -F "instance=${inst}" "${INCUS_DIR}/incus.log" 2>/dev/null | tail -n5 >&2 || true
        return 1
    fi
}

test_security_regression_template_traversal() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local work f ex arch

    work="${INCUS_DIR}/secreg-tpl"
    rm -rf "${work}"
    mkdir -p "${work}"
    rm -f /tmp/secreg-tpl-pwned

    # A real container base to tamper with. Templates apply inside the start
    # hook (forklxc), so a rejection surfaces as a start failure with the real
    # cause recorded in the daemon log. init/start read stdin, fed /dev/null.
    incus image export testimage "${work}/base"
    f="$(find "${work}" -maxdepth 1 -name 'base*' | head -1)"
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    arch="$(awk -F'"' '/"architecture"/ {print $4; exit}' "${ex}/metadata.yaml")"
    rm -rf "${ex}"

    # GHSA-7f67-crqm-jgh7 / CVE-2026-23954: a template whose source path escaped
    # the template directory let the daemon read an arbitrary host file as the
    # template and render its content into the instance.
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    secreg_tpl_metadata "${ex}/metadata.yaml" "${arch}" "/output" "../../../../../../etc/hostname" create
    tar czf "${work}/tpl-read.tar.gz" -C "${ex}" .
    rm -rf "${ex}"
    incus image import "${work}/tpl-read.tar.gz" --alias secreg-tpl-read
    incus init secreg-tpl-read secreg-tplread < /dev/null
    if incus start secreg-tplread < /dev/null >/dev/null 2>&1; then
        echo "FAIL: template source traversal was accepted at start" >&2
        incus delete -f secreg-tplread 2>/dev/null || true
        incus image delete secreg-tpl-read 2>/dev/null || true
        false
    fi
    secreg_tpl_rejected secreg-tplread "attempting to read outside of template directory"
    incus delete -f secreg-tplread 2>/dev/null || true
    incus image delete secreg-tpl-read
    secreg_assert_alive

    # GHSA-83xr-5xxr-mh92 / CVE-2026-33897: a template whose target path escaped
    # the instance rootfs let the daemon write rendered content anywhere on the
    # host as root. The write is now confined to the rootfs via os.Root.
    ex="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${f}" -C "${ex}"
    secreg_tpl_metadata "${ex}/metadata.yaml" "${arch}" "/../../../../../../tmp/secreg-tpl-pwned" "template.tpl" create
    tar czf "${work}/tpl-write.tar.gz" -C "${ex}" .
    rm -rf "${ex}"
    incus image import "${work}/tpl-write.tar.gz" --alias secreg-tpl-write
    incus init secreg-tpl-write secreg-tplwrite < /dev/null
    if incus start secreg-tplwrite < /dev/null >/dev/null 2>&1; then
        echo "FAIL: template target traversal was accepted at start" >&2
        incus delete -f secreg-tplwrite 2>/dev/null || true
        incus image delete secreg-tpl-write 2>/dev/null || true
        false
    fi
    secreg_tpl_rejected secreg-tplwrite "path escapes from parent"
    incus delete -f secreg-tplwrite 2>/dev/null || true
    incus image delete secreg-tpl-write
    secreg_assert_alive

    rm -rf "${work}"
    [ ! -e /tmp/secreg-tpl-pwned ] || { echo "FAIL: a template traversal wrote /tmp/secreg-tpl-pwned" >&2; rm -f /tmp/secreg-tpl-pwned; false; }
}

test_security_regression_vm_template_traversal() {
    # GHSA-4qxq-p5hm-3q3p (CVE pending): the traversal guard added to the LXC
    # driver for CVE-2026-48752 was missing from the QEMU driver, so a VM image
    # whose template source path escaped the template directory let the daemon
    # read an arbitrary host file while generating the VM's config share. The
    # guard is now shared by both drivers.

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    # shellcheck disable=SC2039,3043
    local work out

    work="${INCUS_DIR}/secreg-4qxq"
    rm -rf "${work}"
    mkdir -p "${work}"

    # A minimal VM image whose metadata carries a start template reading a host
    # file outside the template directory. A tiny qcow2 rootfs is enough: the
    # template is applied host-side in generateConfigShare, before QEMU runs.
    printf 'architecture: %s\ncreation_date: 1700000000\nproperties:\n  description: secreg-4qxq\n  os: secreg\ntemplates:\n  /output:\n    when:\n      - start\n    template: ../../../../../../etc/hostname\n' "$(uname -m)" > "${work}/metadata.yaml"
    tar cf "${work}/meta.tar" -C "${work}" metadata.yaml
    qemu-img create -f qcow2 "${work}/rootfs.img" 16M >/dev/null 2>&1

    incus image import "${work}/meta.tar" "${work}/rootfs.img" --alias secreg-4qxq < /dev/null
    incus init secreg-4qxq secreg-4qxqvm --vm < /dev/null

    out="$(incus start secreg-4qxqvm < /dev/null 2>&1 || true)"
    incus stop -f secreg-4qxqvm 2>/dev/null || true
    incus delete -f secreg-4qxqvm 2>/dev/null || true
    incus image delete secreg-4qxq 2>/dev/null || true
    rm -rf "${work}"

    if ! printf '%s\n' "${out}" | grep -qF "attempting to read outside of template directory"; then
        echo "FAIL: VM template source traversal was not rejected at start" >&2
        printf '  start output: %s\n' "${out}" >&2
        false
        return
    fi

    secreg_assert_alive
}

test_security_regression_vm_screenshot_symlink() {
    # GHSA-q9vp-3wcg-8p4x / CVE-2026-33711: the VM VGA screenshot handler created
    # /tmp/incus_screenshot_<id> with os.Create, which follows a pre-planted
    # symlink and truncates and chmods the target as root. The fix removes the
    # path first and recreates it with O_EXCL, discarding any planted symlink.

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    # shellcheck disable=SC2039,3043
    local work sockpath id captured victim out protected

    work="$(mktemp -d -p "${TEST_DIR}" XXX)"
    sockpath="${INCUS_DIR}/unix.socket"
    victim="${work}/victim"

    printf 'architecture: %s\ncreation_date: 1700000000\nproperties:\n  description: secreg-q9vp\n  os: secreg\n' "$(uname -m)" > "${work}/metadata.yaml"
    tar cf "${work}/meta.tar" -C "${work}" metadata.yaml
    qemu-img create -f qcow2 "${work}/rootfs.img" 64M >/dev/null 2>&1

    incus image import "${work}/meta.tar" "${work}/rootfs.img" --alias secreg-q9vp < /dev/null
    incus init secreg-q9vp secreg-q9vpvm --vm < /dev/null

    # A blank framebuffer is enough.
    incus start secreg-q9vpvm < /dev/null

    # The screenshot path is derived from the instance's database ID.
    id="$(incus admin sql global --format csv,noheader "SELECT id FROM instances WHERE name = 'secreg-q9vpvm'")"
    if [ -z "${id}" ]; then
        echo "FAIL: could not look up the instance ID for the screenshot path" >&2
        false
    fi

    captured="/tmp/incus_screenshot_${id}"

    # The attack needs symlink following, which the kernel blocks by default in
    # sticky world-writable dirs; the advisory only bites where it is disabled.
    protected="$(cat /proc/sys/fs/protected_symlinks 2>/dev/null || echo 1)"
    echo 0 > /proc/sys/fs/protected_symlinks 2>/dev/null || true

    printf 'KEEPKEEPKEEP' > "${victim}"
    ln -sf "${victim}" "${captured}"

    curl -k -s --unix-socket "${sockpath}" "incus/1.0/instances/secreg-q9vpvm/console?type=vga" -o /dev/null 2>/dev/null || true

    out="$(cat "${victim}" 2>/dev/null || true)"

    printf '%s\n' "${protected}" > /proc/sys/fs/protected_symlinks 2>/dev/null || true

    # The fixed handler removes the planted symlink before creating its own file.
    if [ -L "${captured}" ]; then
        echo "FAIL: the screenshot handler left the planted symlink in place" >&2
        rm -f "${captured}"
        false
    fi

    rm -f "${captured}"
    incus stop -f secreg-q9vpvm 2>/dev/null || true
    incus delete -f secreg-q9vpvm 2>/dev/null || true
    incus image delete secreg-q9vp 2>/dev/null || true
    rm -rf "${work}"

    if [ "${out}" != "KEEPKEEPKEEP" ]; then
        echo "FAIL: VM screenshot followed a planted symlink and clobbered the target" >&2
        false
        return
    fi

    secreg_assert_alive
}

test_security_regression_nbd_socket() {
    # GHSA-3rgf-297m-775x: the NBD servers Incus runs to migrate or export a VM's
    # disk bound abstract unix sockets, which live in the network namespace with no
    # permission check, so any local user could connect while a transfer ran and
    # read or write the disk. The sockets are now path sockets in the root only run
    # directory. The regression signal is the inverse of the vulnerability: while an
    # NBD session is live, the server's socket exists as a filesystem path, not as an
    # abstract socket. A revert to abstract sockets leaves no such path behind.
    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    # shellcheck disable=SC2039,3043
    local work port nbdpid sock i

    work="$(mktemp -d -p "${TEST_DIR}" XXX)"

    # A blank VM is enough: the NBD export works on QEMU's attached block devices
    # through QMP and does not need a guest OS or agent.
    printf 'architecture: %s\ncreation_date: 1700000000\nproperties:\n  description: secreg-3rgf\n  os: secreg\n' "$(uname -m)" > "${work}/metadata.yaml"
    tar cf "${work}/meta.tar" -C "${work}" metadata.yaml
    qemu-img create -f qcow2 "${work}/rootfs.img" 64M >/dev/null 2>&1

    incus image import "${work}/meta.tar" "${work}/rootfs.img" --alias secreg-3rgf < /dev/null
    incus init secreg-3rgf secreg-3rgfvm --vm < /dev/null
    incus start secreg-3rgfvm < /dev/null

    # Expose the VM's disks over a local NBD proxy; the backend NBD server starts on
    # the first client connection and stays up while that connection is held.
    secreg_incus low-level nbd secreg-3rgfvm --address 127.0.0.1:0 > "${work}/nbd.out" 2>&1 &
    nbdpid=$!

    port=""
    for i in $(seq 1 50); do
        port="$(sed -n 's|.*NBD listening on 127.0.0.1:\([0-9]*\).*|\1|p' "${work}/nbd.out" 2>/dev/null | head -1)"
        [ -n "${port}" ] && break
        sleep 0.2
    done

    if [ -z "${port}" ]; then
        echo "FAIL: the NBD proxy did not announce a listening port" >&2
        cat "${work}/nbd.out" >&2 2>/dev/null || true
        kill "${nbdpid}" 2>/dev/null || true
        incus delete -f secreg-3rgfvm 2>/dev/null || true
        incus image delete secreg-3rgf 2>/dev/null || true
        false
        return
    fi

    # Hold a connection open so the backend NBD server stays up while it is checked.
    # The helper touches "connected" once attached and exits when "release" appears.
    python3 - "${port}" "${work}/connected" "${work}/release" <<'EOF' &
import os, socket, sys, time

port, connected, release = int(sys.argv[1]), sys.argv[2], sys.argv[3]
s = socket.create_connection(("127.0.0.1", port), timeout=10)
open(connected, "w").close()
for _ in range(600):
    if os.path.exists(release):
        break
    time.sleep(0.1)
s.close()
EOF

    for i in $(seq 1 50); do
        [ -e "${work}/connected" ] && break
        sleep 0.2
    done

    # Give the backend server a moment to bind after the proxy accepted the client.
    sock=""
    for i in $(seq 1 50); do
        sock="$(find "${INCUS_DIR}/run" -name qemu.nbd -type s 2>/dev/null | head -1)"
        [ -n "${sock}" ] && break
        sleep 0.2
    done

    touch "${work}/release"
    kill "${nbdpid}" 2>/dev/null || true
    wait "${nbdpid}" 2>/dev/null || true

    incus delete -f secreg-3rgfvm
    incus image delete secreg-3rgf
    rm -rf "${work}"

    if [ -z "${sock}" ]; then
        echo "FAIL: no NBD path socket appeared under the run directory; the server may be using an abstract socket again" >&2
        false
        return
    fi

    secreg_assert_alive
}

# secreg_hmac HEXKEY DATA
# Prints the lowercase-hex HMAC-SHA256 of DATA under the hex-encoded key.
secreg_hmac() {
    printf '%s' "${2}" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:${1}" -binary | od -An -vtx1 | tr -d ' \n'
}

# secreg_dotdot N
# Prints N repetitions of "../".
secreg_dotdot() {
    # shellcheck disable=SC2039,3043
    local n i out
    n="${1}"
    i=0
    out=""
    while [ "${i}" -lt "${n}" ]; do
        out="../${out}"
        i=$((i + 1))
    done
    printf '%s' "${out}"
}

# secreg_s3_sign ENDPOINT BUCKET ACCESS SECRET KEY QS BODY [EXTRA_HEADER]
# Signs a PUT with SigV4 over host;x-amz-content-sha256;x-amz-date and sends it.
# EXTRA_HEADER, when set, is sent but left out of the signature. Prints the
# response body followed by a trailing "SECREG_HTTP=<code>".
secreg_s3_sign() {
    # shellcheck disable=SC2039,3043
    local endpoint bucket access secret key qs body extra host uri url amz_date date_scope scope body_hash signed canonical canonical_hash sts k sig auth
    endpoint="${1}"
    bucket="${2}"
    access="${3}"
    secret="${4}"
    key="${5}"
    qs="${6}"
    body="${7}"
    extra="${8:-}"

    host="$(printf '%s' "${endpoint}" | sed -E 's#^[a-z]+://([^/]+).*#\1#')"
    uri="/${bucket}/${key}"
    if [ -n "${qs}" ]; then
        url="${endpoint}${uri}?${qs}"
    else
        url="${endpoint}${uri}"
    fi

    amz_date="$(date -u +%Y%m%dT%H%M%SZ)"
    date_scope="$(printf '%s' "${amz_date}" | cut -c1-8)"
    scope="${date_scope}/us-east-1/s3/aws4_request"
    body_hash="$(printf '%s' "${body}" | sha256sum | awk '{print $1}')"
    signed="host;x-amz-content-sha256;x-amz-date"

    canonical="$(printf 'PUT\n%s\n%s\nhost:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n\n%s\n%s' \
        "${uri}" "${qs}" "${host}" "${body_hash}" "${amz_date}" "${signed}" "${body_hash}")"
    canonical_hash="$(printf '%s' "${canonical}" | sha256sum | awk '{print $1}')"
    sts="$(printf 'AWS4-HMAC-SHA256\n%s\n%s\n%s' "${amz_date}" "${scope}" "${canonical_hash}")"

    k="$(printf 'AWS4%s' "${secret}" | od -An -vtx1 | tr -d ' \n')"
    k="$(secreg_hmac "${k}" "${date_scope}")"
    k="$(secreg_hmac "${k}" "us-east-1")"
    k="$(secreg_hmac "${k}" "s3")"
    k="$(secreg_hmac "${k}" "aws4_request")"
    sig="$(secreg_hmac "${k}" "${sts}")"
    auth="AWS4-HMAC-SHA256 Credential=${access}/${scope},SignedHeaders=${signed},Signature=${sig}"

    set -- -ksS -w '\nSECREG_HTTP=%{http_code}' -X PUT "${url}" \
        -H "Host: ${host}" -H "X-Amz-Date: ${amz_date}" \
        -H "X-Amz-Content-Sha256: ${body_hash}" -H "Authorization: ${auth}"
    if [ -n "${extra}" ]; then
        set -- "$@" -H "${extra}"
    fi
    set -- "$@" --data-binary "${body}"
    curl "$@"
}

test_security_regression_bucket_security() {
    # shellcheck disable=SC2039,3043
    local incus_backend poolName buckets_addr loop_file_1 loop_device_1 mnt endpoint ak sk out code up upenc
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus_backend="$(storage_backend "$INCUS_DIR")"
    poolName="$(incus profile device get default root pool)"
    mnt=""

    # Buckets on a dir pool need a real filesystem (the suite may run on tmpfs).
    if [ "${incus_backend}" = "dir" ]; then
        configure_loop_device loop_file_1 loop_device_1
        mkfs.ext4 "${loop_device_1}"
        mnt="${TEST_DIR}/secreg-s3sig-mnt"
        mkdir "${mnt}"
        mount "${loop_device_1}" "${mnt}"
        losetup -d "${loop_device_1}"
        mkdir "${mnt}/s3"
        incus storage create secreg-s3sig dir source="${mnt}/s3"
        poolName="secreg-s3sig"
    fi

    buckets_addr="127.0.0.1:$(local_tcp_port)"
    incus config set core.storage_buckets_address "${buckets_addr}"
    endpoint="https://${buckets_addr}"

    ak="SECREGS3ACCESS0001"
    sk="secregs3secretkey0001"
    incus storage bucket create "${poolName}" secreg-s3bkt
    incus storage bucket key create "${poolName}" secreg-s3bkt signer --role=admin --access-key="${ak}" --secret-key="${sk}" >/dev/null

    # Sanity: a correctly signed request is accepted, so the rejections below are
    # the security checks firing and not a broken signing helper.
    out="$(secreg_s3_sign "${endpoint}" secreg-s3bkt "${ak}" "${sk}" sanitykey "" "hello")"
    code="$(printf '%s' "${out}" | sed -n 's/.*SECREG_HTTP=//p')"
    if [ "${code}" != "200" ]; then
        echo "FAIL: a valid SigV4 PutObject was rejected (HTTP ${code}); signing helper is broken" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    # GHSA-ccjc-4qc3-jxqc / CVE-2026-48753: the S3 multipart upload ID was joined
    # to the uploads directory unsanitized, so a ../ upload ID let a signed part
    # write an arbitrary file on the host. The ID is now confined via os.Root.
    rm -rf /tmp/secreg-s3pwned
    mkdir /tmp/secreg-s3pwned
    up="$(secreg_dotdot 50)tmp/secreg-s3pwned"
    upenc="$(printf '%s' "${up}" | sed 's#/#%2F#g')"
    out="$(secreg_s3_sign "${endpoint}" secreg-s3bkt "${ak}" "${sk}" anykey "partNumber=1&uploadId=${upenc}" "marker")"
    code="$(printf '%s' "${out}" | sed -n 's/.*SECREG_HTTP=//p')"
    if [ "${code}" = "200" ] || [ -e /tmp/secreg-s3pwned/part-00001 ]; then
        echo "FAIL: S3 multipart upload ID traversal was accepted (HTTP ${code})" >&2
        printf '  got: %s\n' "${out}" >&2
        rm -rf /tmp/secreg-s3pwned
        false
    fi
    rm -rf /tmp/secreg-s3pwned
    secreg_assert_alive

    # GHSA-mmj7-8rgf-mx2h / CVE-2026-115374: x-amz-* headers not covered by the
    # signature were honored, so an unsigned X-Amz-Copy-Source on an otherwise
    # valid request copied another object onto the target key.
    out="$(secreg_s3_sign "${endpoint}" secreg-s3bkt "${ak}" "${sk}" targetkey "" "" "X-Amz-Copy-Source: secreg-s3bkt/sanitykey")"
    if ! printf '%s\n' "${out}" | grep -qiE 'must be signed|accessdenied'; then
        echo "FAIL: an unsigned X-Amz-Copy-Source header was not rejected" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi
    secreg_assert_alive

    # GHSA-8r3h-vc36-6xpx, GHSA-x8gj-2q73-qr6j / CVE-2026-115371. A restricted TLS
    # cert gets read-only access to the default project's inherited resources, but
    # only when reached through a project it is trusted for. secreg-x8p disables the
    # buckets feature so it inherits the bucket created in the default project above.
    # (The view/edit split that x8gj's key endpoints and pf5w's recursive key
    # stripping rely on needs a fine-grained authorizer; a TLS cert has full access
    # within its own projects, so those are covered by the OpenFGA suite instead.)
    incus project create secreg-x8p -c features.storage.buckets=false
    secreg_trust_restricted secreg-x8 secreg-x8p

    # GHSA-8r3h-vc36-6xpx: the inherited read is only allowed when reached through a
    # project the cert is trusted for. The cert may view the bucket through
    # secreg-x8p (which inherits it)...
    secreg_cert_allowed secreg-x8 "/1.0/storage-pools/${poolName}/buckets/secreg-s3bkt?project=secreg-x8p"
    # ...but not by addressing the default project directly.
    secreg_cert_forbidden secreg-x8 "/1.0/storage-pools/${poolName}/buckets/secreg-s3bkt?project=default"
    # GHSA-x8gj-2q73-qr6j: the key endpoints require can_edit, so the inherited
    # default project exposes no S3 credentials to the restricted cert.
    secreg_cert_forbidden secreg-x8 "/1.0/storage-pools/${poolName}/buckets/secreg-s3bkt/keys?project=default"
    secreg_cert_forbidden secreg-x8 "/1.0/storage-pools/${poolName}/buckets/secreg-s3bkt/keys/signer?project=default"

    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-x8.crt")"
    incus project delete secreg-x8p
    rm -f "${INCUS_CONF}/secreg-x8.crt" "${INCUS_CONF}/secreg-x8.key"
    secreg_assert_alive

    incus storage bucket delete "${poolName}" secreg-s3bkt
    incus config unset core.storage_buckets_address
    if [ "${incus_backend}" = "dir" ]; then
        incus storage delete secreg-s3sig
        [ -z "${mnt}" ] || umount "${mnt}" 2>/dev/null || true
        deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    fi
}

test_security_regression_systemd_credential() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local key out

    rm -f /tmp/secreg-cred-pwned
    incus init testimage secreg-credinst < /dev/null

    # GHSA-q4q8-7f2j-9h9f / CVE-2026-33945: a systemd credential name may itself
    # contain periods and slashes, so "systemd.credential.../../../path" escaped
    # the per-instance credentials directory and wrote an arbitrary host file as
    # root at start. The write is now confined to that directory via os.Root.
    key="systemd.credential.$(secreg_dotdot 12)tmp/secreg-cred-pwned"
    incus config set secreg-credinst "${key}=pwned"
    out="$(incus start secreg-credinst < /dev/null 2>&1 || true)"
    incus delete -f secreg-credinst 2>/dev/null || true
    if ! printf '%s\n' "${out}" | grep -qF "path escapes from parent"; then
        echo "FAIL: systemd credential name traversal was not rejected at start" >&2
        printf '  got: %s\n' "${out}" >&2
        rm -f /tmp/secreg-cred-pwned
        false
    fi
    secreg_assert_alive

    [ ! -e /tmp/secreg-cred-pwned ] || { echo "FAIL: a systemd credential wrote /tmp/secreg-cred-pwned" >&2; rm -f /tmp/secreg-cred-pwned; false; }
}

test_security_regression_rebuild_idmap() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local base i

    # GHSA-9c46-v3hw-v6m8: rebuilding a never-started unprivileged container cleared
    # all three idmap volatile keys, and the start-time check only reallocated a map
    # when one already existed, so the container booted with an identity map and ran
    # as privileged (container root = host root). A map must be allocated when none
    # is set. Inside the guest the uid map must therefore not map root to host uid 0.
    incus init testimage secreg-rb < /dev/null
    incus rebuild testimage secreg-rb < /dev/null
    incus start secreg-rb < /dev/null

    base=""
    for i in $(seq 1 30); do
        base="$(incus exec secreg-rb -- cat /proc/self/uid_map 2>/dev/null | awk 'NR==1{print $2}')"
        [ -n "${base}" ] && break
        sleep 0.5
    done
    incus delete -f secreg-rb

    if [ -z "${base}" ]; then
        echo "FAIL: could not read the container uid map after rebuild and start" >&2
        false
    fi
    if [ "${base}" = "0" ]; then
        echo "FAIL: rebuilt container booted with an identity idmap (running privileged)" >&2
        false
    fi
}

test_security_regression_operation_cancel() {
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # shellcheck disable=SC2039,3043
    local opid

    # GHSA-cwg9-8mc4-hf7j: operations with no specific permission (such as the token
    # operations behind trust tokens) could be cancelled by any authenticated client
    # by UUID. Cancellation is now limited to the operation's requestor or a client
    # that can edit the project. A restricted certificate scoped elsewhere must not
    # be able to cancel the pending token operation.
    incus project create secreg-opcp
    secreg_trust_restricted secreg-opout secreg-opcp

    incus config trust add secreg-optok >/dev/null
    opid=""
    for _ in $(seq 1 20); do
        opid="$(incus operation list --all-projects --format csv 2>/dev/null | awk -F, '$2 == "TOKEN" && $4 == "RUNNING" { print $1; exit }')"
        [ -n "${opid}" ] && break
        sleep 0.5
    done
    if [ -z "${opid}" ]; then
        echo "FAIL: could not obtain a pending token operation id" >&2
        false
    fi

    secreg_cert_forbidden secreg-opout "/1.0/operations/${opid}" -X DELETE

    incus operation delete "${opid}" 2>/dev/null || true
    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-opout.crt")"
    incus project delete secreg-opcp
    rm -f "${INCUS_CONF}/secreg-opout.crt" "${INCUS_CONF}/secreg-opout.key"
}

test_security_regression_operation_secret() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local sock create opid control getop

    # GHSA-h2j8-4crh-vhfc: websocket operations (migration, exec, console) embed
    # their per-connection secrets in the operation metadata. These were broadcast
    # to every event listener and returned by operation queries, so a project event
    # viewer could read a migration's secret and hijack its data channels. The
    # secrets are now returned only in the synchronous create response and stripped
    # from every other render. Exec exercises the same render path without a live
    # migration: its metadata carries the "fds" secret map.
    incus launch testimage secreg-opsec
    sock="${INCUS_DIR}/unix.socket"

    create="$(curl -s --unix-socket "${sock}" -X POST \
        -d '{"command":["/bin/sh"],"wait-for-websocket":true,"interactive":false,"environment":{}}' \
        incus/1.0/instances/secreg-opsec/exec)"

    # The create response is the one-time secret delivery, so it must carry the
    # secrets; otherwise their absence from the query below would prove nothing.
    control="$(printf '%s\n' "${create}" | jq -r '.metadata.metadata.fds.control // empty')"
    if [ -z "${control}" ]; then
        echo "FAIL: the exec create response did not contain the websocket secrets" >&2
        printf '  got: %s\n' "${create}" >&2
        false
    fi

    opid="$(printf '%s\n' "${create}" | jq -r '.metadata.id')"

    getop="$(curl -s --unix-socket "${sock}" "incus/1.0/operations/${opid}")"

    # Querying the operation must not return the secrets.
    if printf '%s\n' "${getop}" | jq -e '.metadata.metadata.fds' >/dev/null 2>&1; then
        echo "FAIL: querying the operation leaked the websocket secrets" >&2
        printf '  got: %s\n' "${getop}" >&2
        false
    fi

    # Sanity: the operation is still rendered (its other metadata is present), so
    # the absence above is the strip and not a missing or empty operation.
    if ! printf '%s\n' "${getop}" | jq -e '.metadata.metadata.command' >/dev/null 2>&1; then
        echo "FAIL: the operation query did not return the expected metadata" >&2
        printf '  got: %s\n' "${getop}" >&2
        false
    fi

    incus operation delete "${opid}" 2>/dev/null || true
    incus delete -f secreg-opsec
}

test_security_regression_guestapi_image() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local fp_other fp_local out

    # Build the /dev/incus guest-API client; busybox has no HTTP-over-unix-socket
    # tool, so the request from inside the container goes through this helper.
    (
        cd dev_incus-client || return
        go build -tags netgo -buildvcs=false ./...
    )

    # GHSA-f7xp-22ph-v444: the guest-API image export forwarded the guest-controlled
    # "project" query parameter straight to imageExport, so a container with
    # security.guestapi.images could export any public image of any project. The
    # project is now pinned to the instance's own project.

    # A distinct public image present only in another project.
    incus project create secreg-gimg -c features.images=true -c features.profiles=false
    incus image copy testimage local: --project default --target-project secreg-gimg --alias secreg-base
    incus init secreg-base secreg-gpub --project secreg-gimg
    incus publish secreg-gpub --project secreg-gimg --alias secreg-other --public
    incus delete secreg-gpub --project secreg-gimg
    # The guest-API export matches on the exact (full) fingerprint.
    fp_other="$(incus image info secreg-other --project secreg-gimg | awk '/^Fingerprint:/ {print $2}')"

    # A distinct public image in the container's own (default) project, for the
    # positive control below.
    incus init testimage secreg-lpub
    incus publish secreg-lpub --alias secreg-local --public
    incus delete secreg-lpub
    fp_local="$(incus image info secreg-local | awk '/^Fingerprint:/ {print $2}')"

    incus launch testimage secreg-gctr -c security.guestapi=true -c security.guestapi.images=true
    incus file push --mode 0755 dev_incus-client/dev_incus-client secreg-gctr/bin/

    # Sanity: a public image in the container's own project exports, so the
    # cross-project rejection below is the pin and not a broken client.
    out="$(incus exec secreg-gctr -- dev_incus-client image-export "${fp_local}" default)"
    if [ "${out}" != "status=200" ]; then
        echo "FAIL: guest-API export of an in-project public image did not return 200" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    # The attack: the other project's image does not exist in the instance's own
    # project, so pinning must turn the export into a not-found instead of leaking it.
    out="$(incus exec secreg-gctr -- dev_incus-client image-export "${fp_other}" secreg-gimg)"
    if [ "${out}" != "status=404" ]; then
        echo "FAIL: the guest API exported an image from another project" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    incus delete -f secreg-gctr
    incus image delete secreg-local
    incus image delete secreg-other --project secreg-gimg
    incus image delete secreg-base --project secreg-gimg
    incus project delete secreg-gimg
    rm -f dev_incus-client/dev_incus-client
}

test_security_regression_networkacl_events() {
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # shellcheck disable=SC2039,3043
    local out

    # GHSA-9f75-5fjw-5jvv: OVN network-acl log events are broadcast with an empty
    # project, so the per-project permission check in the event server is skipped
    # and a client with view-events on any single project received every project's
    # ACL logs. network-acl is now a privileged event type (like logging): a client
    # without server-level privileged-event access is refused when it asks for it.
    incus project create secreg-aclp
    secreg_trust_restricted secreg-aclc secreg-aclp

    secreg_cert_forbidden secreg-aclc "/1.0/events?project=secreg-aclp&type=network-acl"

    # Sanity: the same restricted cert may subscribe to a non-privileged event type
    # on its project, so the rejection above is the privileged-type gate and not a
    # blanket events or project denial. The stream stays open, so bound the wait.
    out="$(curl -k -s --max-time 3 \
        --cert "${INCUS_CONF}/secreg-aclc.crt" --key "${INCUS_CONF}/secreg-aclc.key" \
        "https://${INCUS_ADDR}/1.0/events?project=secreg-aclp&type=lifecycle" 2>/dev/null || true)"
    if printf '%s\n' "${out}" | grep -q '"error_code":403'; then
        echo "FAIL: the restricted cert could not subscribe to a non-privileged event type (setup/authz issue)" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-aclc.crt")"
    incus project delete secreg-aclp
    rm -f "${INCUS_CONF}/secreg-aclc.crt" "${INCUS_CONF}/secreg-aclc.key"
}

test_security_regression_guestapi_cgroup() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local out

    # Build the guest-API client; busybox has no HTTP-over-unix-socket tool.
    (
        cd dev_incus-client || return
        go build -tags netgo -buildvcs=false ./...
    )

    # GHSA-v736-5xp3-jghf: the guest API maps a connection to its instance from the
    # peer PID, falling back to the process cgroup path. The fallback accepted the
    # first "lxc.monitor." found anywhere in the path, so a container could create a
    # cgroup named after another container below its own and be served that other
    # container's guest API. Only the first lxc.payload./lxc.monitor. component is
    # trusted now. Both instances share the default idmap so the guest-API uid check
    # (root maps to the same host uid) does not mask the misidentification.
    incus launch testimage secreg-cgvictim
    incus launch testimage secreg-cgattack
    incus file push --mode 0755 dev_incus-client/dev_incus-client secreg-cgattack/bin/

    # The attack script forges a cgroup named after the victim, moves the querying
    # process into it, then hits the guest API. It is pushed as a file (not an inline
    # exec) to survive the test incus wrapper's argument handling. meta-data names
    # the identified instance; the SETUP_FAIL markers ensure the attack was actually
    # staged, so a pass can't come from the forged cgroup silently not taking effect.
    cat > "${INCUS_DIR}/secreg-cgattack.sh" << 'SCRIPT'
#!/bin/sh
mkdir -p /sys/fs/cgroup/lxc.monitor.secreg-cgvictim 2>/dev/null || { echo SETUP_FAIL_MKDIR; exit 0; }
echo $$ > /sys/fs/cgroup/lxc.monitor.secreg-cgvictim/cgroup.procs 2>/dev/null || { echo SETUP_FAIL_MOVE; exit 0; }
grep -q lxc.monitor.secreg-cgvictim /proc/self/cgroup || { echo SETUP_FAIL_APPLY; exit 0; }
exec dev_incus-client meta-data
SCRIPT
    incus file push --mode 0755 "${INCUS_DIR}/secreg-cgattack.sh" secreg-cgattack/root/secreg-cgattack.sh
    out="$(secreg_incus exec secreg-cgattack -- /root/secreg-cgattack.sh)"

    case "${out}" in
        *SETUP_FAIL*)
            echo "FAIL: could not stage the forged cgroup inside the container" >&2
            printf '  got: %s\n' "${out}" >&2
            false
            ;;
    esac

    if printf '%s\n' "${out}" | grep -q 'local-hostname: secreg-cgvictim'; then
        echo "FAIL: a forged cgroup made the guest API serve another instance" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    if ! printf '%s\n' "${out}" | grep -q 'local-hostname: secreg-cgattack'; then
        echo "FAIL: the guest API did not identify the connecting container as itself" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    incus delete -f secreg-cgattack secreg-cgvictim
    rm -f dev_incus-client/dev_incus-client "${INCUS_DIR}/secreg-cgattack.sh"
}

test_security_regression_s3_signed_payload() {
    # shellcheck disable=SC2039,3043
    local incus_backend poolName buckets_addr loop_file_1 loop_device_1 mnt ak sk
    # shellcheck disable=SC2039,3043
    local pid baseline peak growth body bodyHash auth date code curlpid cur i

    incus_backend="$(storage_backend "$INCUS_DIR")"
    case "${incus_backend}" in
        ceph | linstor)
            echo "==> SKIP: Local storage needed for the S3 bucket server"
            export TEST_UNMET_REQUIREMENT="local storage needed for the S3 bucket server"
            return
            ;;
    esac

    # GHSA-8mgx-h4v8-xqv2: the local S3 server verified a signed-payload request by
    # reading the whole body into memory to check its hash before verifying the
    # signature. Anyone knowing a valid access key could make the daemon buffer an
    # arbitrarily large body. The signature is now checked first and the body is
    # hashed as it streams, so a bad-signature request is rejected without buffering.
    poolName="$(incus profile device get default root pool)"
    mnt=""

    # Buckets on a dir pool need a real filesystem (the suite may run on tmpfs).
    if [ "${incus_backend}" = "dir" ]; then
        configure_loop_device loop_file_1 loop_device_1
        mkfs.ext4 "${loop_device_1}"
        mnt="${TEST_DIR}/secreg-s3dos-mnt"
        mkdir "${mnt}"
        mount "${loop_device_1}" "${mnt}"
        losetup -d "${loop_device_1}"
        mkdir "${mnt}/s3"
        incus storage create secreg-s3dos dir source="${mnt}/s3"
        poolName="secreg-s3dos"
    fi

    buckets_addr="127.0.0.1:$(local_tcp_port)"
    incus config set core.storage_buckets_address "${buckets_addr}"

    ak="SECREGS3DOSKEY0001"
    sk="secregs3dossecret0001"
    incus storage bucket create "${poolName}" secreg-s3dos
    incus storage bucket key create "${poolName}" secreg-s3dos signer --role=admin --access-key="${ak}" --secret-key="${sk}" > /dev/null

    # A valid access key, a signed-payload hash (so the body-hash path is taken) and a
    # deliberately wrong signature. The hash need not match the body: pre-fix the body
    # is buffered before that is noticed, post-fix the bad signature is caught first.
    date="20260101T000000Z"
    bodyHash="e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
    auth="AWS4-HMAC-SHA256 Credential=${ak}/20260101/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=0000000000000000000000000000000000000000000000000000000000000000"

    # Sanity: a small request with this bad signature is rejected (4xx), confirming the
    # endpoint is live, the access key is known and the request reaches authentication.
    code="$(curl -k -s -o /dev/null -w '%{http_code}' -X PUT \
        -H "Expect:" -H "X-Amz-Date: ${date}" -H "X-Amz-Content-Sha256: ${bodyHash}" \
        -H "Authorization: ${auth}" --data-binary "probe" \
        "https://${buckets_addr}/secreg-s3dos/secreg-probe")"
    case "${code}" in
        4*) ;;
        *)
            echo "FAIL: probe request was not rejected (http ${code}); test setup is broken" >&2
            false
            ;;
    esac

    pid="$(cat "${INCUS_DIR}/incus.pid")"
    body="${TEST_DIR}/secreg-s3dos-body"
    head -c 209715200 /dev/zero > "${body}"

    baseline="$(awk '/^VmRSS:/ {print $2}' "/proc/${pid}/status")"
    baseline="${baseline:-0}"

    # Upload the body slowly so the daemon's memory can be sampled while in flight.
    # Pre-fix the daemon buffers the whole body before rejecting on the signature;
    # post-fix it rejects immediately and never reads the body.
    curl -k -s -o /dev/null --max-time 30 --limit-rate 50M -X PUT \
        -H "Expect:" -H "X-Amz-Date: ${date}" -H "X-Amz-Content-Sha256: ${bodyHash}" \
        -H "Authorization: ${auth}" -T "${body}" \
        "https://${buckets_addr}/secreg-s3dos/secreg-dos" &
    curlpid=$!

    peak="${baseline}"
    i=0
    while kill -0 "${curlpid}" 2>/dev/null && [ "${i}" -lt 2000 ]; do
        cur="$(awk '/^VmRSS:/ {print $2}' "/proc/${pid}/status" 2>/dev/null)"
        cur="${cur:-0}"
        if [ "${cur}" -gt "${peak}" ]; then
            peak="${cur}"
        fi

        i=$((i + 1))
        sleep 0.05
    done
    wait "${curlpid}" 2>/dev/null || true

    cur="$(awk '/^VmRSS:/ {print $2}' "/proc/${pid}/status" 2>/dev/null)"
    cur="${cur:-0}"
    if [ "${cur}" -gt "${peak}" ]; then
        peak="${cur}"
    fi

    growth=$((peak - baseline))
    echo "==> s3_signed_payload RSS baseline=${baseline}kB peak=${peak}kB growth=${growth}kB"
    if [ "${growth}" -gt 51200 ]; then
        echo "FAIL: a 200MiB bad-signature request grew incusd RSS by ${growth}kB (buffered body)" >&2
        false
    fi

    secreg_assert_alive

    rm -f "${body}"
    incus storage bucket delete "${poolName}" secreg-s3dos
    incus config unset core.storage_buckets_address
    if [ "${incus_backend}" = "dir" ]; then
        incus storage delete secreg-s3dos
        [ -z "${mnt}" ] || umount "${mnt}" 2>/dev/null || true
        deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    fi
}

test_security_regression_copy_privileged() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local pool

    pool="$(incus storage list --format csv --columns n | head -n1)"

    # GHSA-mq9x-prm8-3vpw / CVE-2026-62941: on a cross-project instance copy the
    # project restriction check ran only BEFORE the source instance's config was
    # merged into the request, so a privileged source could be copied into a
    # restricted project and carry security.privileged past the restriction. The
    # merged config is re-checked now.
    #
    # The request is sent raw with an empty config: the CLI would forward the source
    # config itself, which the pre-merge check already catches, so that would not
    # exercise the vulnerable path. With config empty the privileged key is only
    # introduced by the server-side source merge. A local root disk on the source is
    # carried by that same merge, so with no profiles the only restricted key is
    # security.privileged, making "Privileged containers are forbidden" the sole
    # possible rejection rather than an unrelated device restriction.
    incus project create secreg-mq9x -c restricted=true

    incus init testimage secreg-mq9x-src -c security.privileged=true
    incus config device add secreg-mq9x-src root disk pool="${pool}" path=/

    secreg_query_reject 'Privileged containers are forbidden' POST "/1.0/instances?project=secreg-mq9x" \
        '{"name":"secreg-mq9x-dst","source":{"type":"copy","source":"secreg-mq9x-src","project":"default"},"profiles":[],"config":{},"devices":{}}'

    if incus info secreg-mq9x-dst --project secreg-mq9x > /dev/null 2>&1; then
        echo "FAIL: a privileged instance was copied into a restricted project" >&2
        incus delete -f secreg-mq9x-dst --project secreg-mq9x
        false
    fi

    incus delete -f secreg-mq9x-src
    incus project delete secreg-mq9x
}

test_security_regression_snapshot_restore_privileged() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local pool

    pool="$(incus storage list --format csv --columns n | head -n1)"

    # GHSA-48q5-w887-33wv / CVE-2026-48751: snapshot restore ignored project
    # restrictions, so a snapshot carrying a restricted key (raw.lxc, raw.qemu,
    # security.privileged, ...) could be restored in a restricted project to run a
    # low-level or privileged instance and execute arbitrary commands on the host.
    # Restore now re-checks the snapshot's config against the project restrictions.
    #
    # A snapshot is taken carrying raw.lxc while low-level config is still allowed, the
    # key is cleared, then low-level config is blocked, mirroring a snapshot that holds
    # a now-forbidden key (as produced by crafting an instance elsewhere and moving it
    # in). Restoring it must be rejected. raw.lxc is used rather than security.privileged
    # because the latter also flips the idmap, which trips the volatile-config check on
    # a different key first; raw.lxc changes nothing volatile, isolating the low-level
    # restriction. Restore reverts config in place rather than re-creating, so this
    # check (not a create-path one) is what blocks it.
    incus project create secreg-48q5 -c restricted=true -c restricted.containers.lowlevel=allow -c restricted.snapshots=allow -c features.images=false
    incus profile device add default root disk pool="${pool}" path=/ --project secreg-48q5
    incus init testimage secreg-48q5-c1 --project secreg-48q5 -c raw.lxc="lxc.environment=SECREG=1"
    incus snapshot create secreg-48q5-c1 snap0 --project secreg-48q5
    incus config unset secreg-48q5-c1 raw.lxc --project secreg-48q5
    incus project set secreg-48q5 restricted.containers.lowlevel=block

    secreg_expect_fail 'Use of low-level config "raw.lxc"' \
        incus snapshot restore secreg-48q5-c1 snap0 --project secreg-48q5

    incus delete -f secreg-48q5-c1 --project secreg-48q5
    incus project delete secreg-48q5
}

test_security_regression_import_body_limit() {
    # shellcheck disable=SC2039,3043
    local out big

    # GHSA-98vh-x9cx-9cfp: many endpoints streamed an unbounded request body into
    # memory or temporary files, so an authenticated user could exhaust the host's
    # disk or memory. Request bodies are now capped at 1 MiB except on endpoints that
    # genuinely need a large body (images, instances, volumes, buckets, file pushes),
    # which opt out via LargeRequest. A 2 MiB body to a normal endpoint (profiles) is
    # read through MaxBytesReader and must be rejected.
    big="${TEST_DIR}/secreg-98vh-body"
    {
        printf '{"name":"secreg-98vh","config":{"user.pad":"'
        head -c 2097152 /dev/zero | tr '\0' 'A'
        printf '"}}'
    } > "${big}"

    out="$(curl -s --unix-socket "${INCUS_DIR}/unix.socket" -X POST -H "Content-Type: application/json" --data-binary @"${big}" incus/1.0/profiles)"
    if ! printf '%s\n' "${out}" | grep -q "request body too large"; then
        echo "FAIL: a 2MiB body on a non-large endpoint was not rejected" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    rm -f "${big}"
    incus profile delete secreg-98vh 2>/dev/null || true
}

test_security_regression_cert_chain() {
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # shellcheck disable=SC2039,3043
    local out

    # GHSA-xg75-xf37-cph9: the HTTPS listener uses tls.RequestClientCert, so the
    # handshake only proves possession of the first certificate in the client's
    # chain. The trust checks looped over the whole chain and matched each entry, so
    # a client could authenticate as any trust entry by appending that entry's
    # certificate to its own chain. Only the first peer certificate is trusted now.
    gen_cert_and_key "${INCUS_CONF}/secreg-trusted.key" "${INCUS_CONF}/secreg-trusted.crt" "secreg-trusted.local"
    incus config trust add-certificate "${INCUS_CONF}/secreg-trusted.crt"
    gen_cert_and_key "${INCUS_CONF}/secreg-attacker.key" "${INCUS_CONF}/secreg-attacker.crt" "secreg-attacker.local"

    # Sanity: the trusted certificate on its own authenticates, so "untrusted" below
    # is the chain being rejected and not some unrelated breakage.
    out="$(curl -k -s --cert "${INCUS_CONF}/secreg-trusted.crt" --key "${INCUS_CONF}/secreg-trusted.key" "https://${INCUS_ADDR}/1.0")"
    if ! printf '%s\n' "${out}" | grep -q '"auth":"trusted"'; then
        echo "FAIL: the trusted certificate did not authenticate; test setup is broken" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    # Present the untrusted attacker certificate as the leaf with the trusted
    # certificate appended; the connection must be treated as untrusted.
    cat "${INCUS_CONF}/secreg-attacker.crt" "${INCUS_CONF}/secreg-trusted.crt" > "${INCUS_CONF}/secreg-chain.crt"
    out="$(curl -k -s --cert "${INCUS_CONF}/secreg-chain.crt" --key "${INCUS_CONF}/secreg-attacker.key" "https://${INCUS_ADDR}/1.0")"
    if ! printf '%s\n' "${out}" | grep -q '"auth":"untrusted"'; then
        echo "FAIL: a trusted certificate appended to an untrusted chain was accepted" >&2
        printf '  got: %s\n' "${out}" >&2
        false
    fi

    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-trusted.crt")"
    rm -f "${INCUS_CONF}/secreg-trusted.crt" "${INCUS_CONF}/secreg-trusted.key" \
        "${INCUS_CONF}/secreg-attacker.crt" "${INCUS_CONF}/secreg-attacker.key" "${INCUS_CONF}/secreg-chain.crt"
}

test_security_regression_image_fingerprint() {
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # shellcheck disable=SC2039,3043
    local body

    # GHSA-p2v3-6wvc-cv3p: imageDownload used the requested fingerprint directly as
    # an on-disk file name. On the remote-copy path a request carrying a secret skips
    # alias resolution, so the raw fingerprint reached the filesystem and a value with
    # path separators could escape the images directory. The fingerprint is now
    # validated as a hex string before any filesystem use. The check runs before the
    # download connects, so a secret request to the local server with a traversal
    # fingerprint is rejected up front.
    body='{"source":{"type":"image","mode":"pull","server":"https://'"${INCUS_ADDR}"'","protocol":"incus","secret":"secreg","fingerprint":"../../../../../../tmp/secreg-p2v3"}}'
    secreg_expect_fail 'Invalid image fingerprint' secreg_incus query --wait -X POST --data "${body}" /1.0/images
    rm -f /tmp/secreg-p2v3.download /tmp/secreg-p2v3.download.rootfs
    secreg_assert_alive
}

test_security_regression_image_cross_project() {
    ensure_import_testimage
    # shellcheck disable=SC2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # shellcheck disable=SC2039,3043
    local fp op out sane

    # GHSA-c6wx-8679-hpr9: imageDownload reused an identical image already present
    # in another project without checking the caller could see it. A client
    # restricted to its own project could therefore pull a private image belonging
    # to a different project by referencing its fingerprint. Reuse now requires the
    # image to be public or the caller to hold "can_view" on it; otherwise it is
    # downloaded afresh to prove access and the reuse shortcut no longer leaks it.
    #
    # The source image must live in a non-default project: default-project images
    # are inherited by every project, so a restricted certificate legitimately holds
    # "can_view" on those and reusing them is not a disclosure.
    fp="$(incus image info testimage | awk '/^Fingerprint:/ {print $2}')"

    incus project create secreg-c6wx-src
    incus project create secreg-c6wx
    incus image copy testimage localhost: --target-project secreg-c6wx-src
    # Remove the default-project copy so the image exists only in the source
    # project, otherwise the inherited-images rule grants the restricted cert a
    # legitimate view and reuse is not a disclosure.
    incus image delete testimage
    secreg_trust_restricted secreg-c6wx secreg-c6wx

    # Sanity: the restricted certificate cannot see the image in the source project,
    # so a successful reuse below is a real cross-project disclosure.
    sane="$(curl -k -s --cert "${INCUS_CONF}/secreg-c6wx.crt" --key "${INCUS_CONF}/secreg-c6wx.key" \
        "https://${INCUS_ADDR}/1.0/images/${fp}?project=secreg-c6wx-src")"
    if printf '%s' "${sane}" | grep -q '"status_code":200'; then
        echo "FAIL: the restricted certificate could already see the source image; setup is broken" >&2
        printf '  got: %s\n' "${sane}" >&2
        false
    fi

    # As the restricted certificate, try to pull the private image into its own
    # project by fingerprint. A secret is set so alias resolution is skipped and a
    # valid protocol is recorded, meaning only the can_view check stops the reuse.
    out="$(curl -k -s --cert "${INCUS_CONF}/secreg-c6wx.crt" --key "${INCUS_CONF}/secreg-c6wx.key" \
        -X POST --data '{"source":{"type":"image","mode":"pull","server":"https://'"${INCUS_ADDR}"'","protocol":"incus","secret":"secreg","fingerprint":"'"${fp}"'"}}' \
        "https://${INCUS_ADDR}/1.0/images?project=secreg-c6wx")"
    op="$(printf '%s' "${out}" | sed -n 's/.*"operation":"\([^"]*\)".*/\1/p')"
    if [ -n "${op}" ]; then
        curl -k -s --cert "${INCUS_CONF}/secreg-c6wx.crt" --key "${INCUS_CONF}/secreg-c6wx.key" \
            "https://${INCUS_ADDR}${op}/wait" >/dev/null
    fi

    if incus image info "${fp}" --project secreg-c6wx >/dev/null 2>&1; then
        echo "FAIL: a restricted client pulled a private cross-project image" >&2
        incus image delete "${fp}" --project secreg-c6wx 2>/dev/null || true
        false
    fi

    incus image delete "${fp}" --project secreg-c6wx-src
    incus project delete secreg-c6wx
    incus project delete secreg-c6wx-src
    incus config trust remove "$(cert_fingerprint "${INCUS_CONF}/secreg-c6wx.crt")"
    rm -f "${INCUS_CONF}/secreg-c6wx.crt" "${INCUS_CONF}/secreg-c6wx.key"
}

test_security_regression_image_url_ssrf() {
    # shellcheck disable=SC2039,3043
    local port marker ready body out listener

    # GHSA-8gw4-p4wq-4hcv: imgPostURLInfo issued the preflight HEAD to the source
    # URL before enforcing the project's restricted.images.servers restriction, so a
    # user in such a project could still make the daemon send HEAD requests to
    # arbitrary hosts (blind SSRF). The restriction is now checked before the HEAD.
    port="$(local_tcp_port)"
    marker="${TEST_DIR}/secreg-8gw4-hit"
    ready="${TEST_DIR}/secreg-8gw4-ready"
    rm -f "${marker}" "${ready}"

    # One-shot listener: records a hit if the daemon connects, i.e. the HEAD was sent.
    python3 - "${port}" "${marker}" "${ready}" <<'EOF' &
import socket, sys
port, marker, ready = int(sys.argv[1]), sys.argv[2], sys.argv[3]
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", port))
s.listen(1)
open(ready, "w").close()
s.settimeout(15)
try:
    conn, _ = s.accept()
    open(marker, "w").write("hit")
    conn.close()
except Exception:
    pass
finally:
    s.close()
EOF
    listener=$!

    # Wait until the listener is bound before triggering the request.
    # shellcheck disable=SC2039,3043
    local i
    i=0
    while [ ! -e "${ready}" ] && [ "${i}" -lt 100 ]; do
        sleep 0.1
        i=$((i + 1))
    done

    incus project create secreg-8gw4 \
        -c restricted=true \
        -c restricted.images.servers=images.example.invalid

    # Import from a URL whose host is not in the allowed list.
    body='{"source":{"type":"url","url":"http://127.0.0.1:'"${port}"'/"}}'
    out="$(secreg_incus query --wait -X POST --data "${body}" "/1.0/images?project=secreg-8gw4" 2>&1 || true)"

    kill "${listener}" 2>/dev/null || true
    wait "${listener}" 2>/dev/null || true

    if [ -e "${marker}" ]; then
        echo "FAIL: a restricted project triggered a preflight HEAD to a disallowed server" >&2
        incus project delete secreg-8gw4 2>/dev/null || true
        rm -f "${marker}" "${ready}"
        false
    fi

    # Sanity: the import was rejected by the image-server restriction, so the absent
    # HEAD is that restriction and not some unrelated failure.
    if ! printf '%s\n' "${out}" | grep -qF "isn't allowed in this project"; then
        echo "FAIL: the URL import was not rejected by the image-server restriction" >&2
        printf '  got: %s\n' "${out}" >&2
        incus project delete secreg-8gw4 2>/dev/null || true
        rm -f "${marker}" "${ready}"
        false
    fi

    incus project delete secreg-8gw4
    rm -f "${marker}" "${ready}"
}

test_security_regression_image_url_hash() {
    # shellcheck disable=SC2039,3043
    local port ready server body out victim i

    # GHSA-f6m5-xw2g-xc4x: a url-source image import issues a preflight HEAD and
    # then downloads using the server-supplied Incus-Image-Hash as the on-disk file
    # name (protocol "direct", os.Create(<hash>.download)). A malicious image server
    # could therefore return a hash with path separators, causing the host to create
    # (truncating any existing file) and, on the inevitable hash mismatch, remove a
    # file outside the images directory. The hash is now validated as a hex
    # fingerprint before any filesystem use.
    port="$(local_tcp_port)"
    ready="${TEST_DIR}/secreg-f6m5-ready"
    # The traversal hash below resolves to this path once ".download" is appended.
    victim="/tmp/secreg-f6m5.download"
    rm -f "${ready}"
    echo "KEEP" > "${victim}"

    # Malicious image server: answers the preflight HEAD with a traversal hash and a
    # download URL, and serves a body for the subsequent GET.
    python3 - "${port}" "${ready}" <<'EOF' &
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port, ready = int(sys.argv[1]), sys.argv[2]


class H(BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.send_response(200)
        self.send_header("Incus-Image-Hash", "../../../../../../../../tmp/secreg-f6m5")
        self.send_header("Incus-Image-URL", "http://127.0.0.1:%d/img" % port)
        self.end_headers()

    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"secreg")

    def log_message(self, *a):
        pass


srv = HTTPServer(("127.0.0.1", port), H)
open(ready, "w").close()
srv.timeout = 20
srv.handle_request()
srv.handle_request()
EOF
    server=$!

    i=0
    while [ ! -e "${ready}" ] && [ "${i}" -lt 100 ]; do
        sleep 0.1
        i=$((i + 1))
    done

    body='{"source":{"type":"url","url":"http://127.0.0.1:'"${port}"'/"}}'
    out="$(secreg_incus query --wait -X POST --data "${body}" /1.0/images 2>&1 || true)"

    kill "${server}" 2>/dev/null || true
    wait "${server}" 2>/dev/null || true

    if [ "$(cat "${victim}" 2>/dev/null)" != "KEEP" ]; then
        echo "FAIL: a malicious image hash truncated or removed a file outside the images directory (${victim})" >&2
        rm -f "${victim}" "${ready}"
        false
    fi

    if ! printf '%s\n' "${out}" | grep -qF "Invalid image fingerprint"; then
        echo "FAIL: a server-supplied traversal image hash was not rejected" >&2
        printf '  got: %s\n' "${out}" >&2
        rm -f "${victim}" "${ready}"
        false
    fi

    rm -f "${victim}" "${ready}"
    secreg_assert_alive
}

test_security_regression_metadata_symlink() {
    # The instance directory is only directly accessible for file-based backends.
    if [ "$(storage_backend "${INCUS_DIR}")" != "dir" ]; then
        echo "==> SKIP: dir storage needed to reach the instance directory"
        export TEST_UNMET_REQUIREMENT="dir storage needed to reach the instance directory"
        return
    fi

    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local victim cdir out

    # GHSA-fmjx-5j3g-997p: the instance metadata API opened metadata.yaml by joining
    # it onto the instance path and followed a symlink there, so an image (or on-disk
    # tampering) whose metadata.yaml points at a host file could disclose and overwrite
    # that file as root. Metadata access is now confined with os.OpenRoot, which
    # refuses a symlink that escapes the instance directory.
    victim="/tmp/secreg-fmjx-secret"
    printf 'architecture: x86_64\nproperties:\n  secreg: SECREGFMJXLEAK\n' > "${victim}"

    incus init testimage secreg-fmjx
    cdir="${INCUS_DIR}/containers/secreg-fmjx"
    rm -f "${cdir}/metadata.yaml"
    ln -s "${victim}" "${cdir}/metadata.yaml"

    out="$(secreg_incus query /1.0/instances/secreg-fmjx/metadata 2>&1 || true)"
    if printf '%s\n' "${out}" | grep -qF "SECREGFMJXLEAK"; then
        echo "FAIL: the metadata API followed a symlink and disclosed a host file" >&2
        printf '  got: %s\n' "${out}" >&2
        incus delete secreg-fmjx --force 2>/dev/null || true
        rm -f "${victim}"
        false
    fi

    incus delete secreg-fmjx --force
    rm -f "${victim}"
    secreg_assert_alive
}

test_security_regression_backup_nil_container() {
    ensure_import_testimage

    # shellcheck disable=SC2039,3043
    local work d
    work="${INCUS_DIR}/secreg-x5r6"
    rm -rf "${work}"
    mkdir -p "${work}"

    # A clean, uncompressed instance backup used as the base to tamper with.
    incus init testimage secreg-x5r6-src
    incus export secreg-x5r6-src "${work}/base.tar" --instance-only --compression none
    incus delete -f secreg-x5r6-src

    # GHSA-x5r6-jr56-89pv: backup.GetInfo() trusts the inline backup/index.yaml, so a
    # backup can pass import preflight with a valid inline config while carrying a
    # legacy backup/container/backup.yaml that omits the container section. During
    # restore that legacy file is reparsed and UpdateInstanceConfig dereferenced the
    # nil Container, crashing the daemon. A missing container is now rejected.
    d="$(mktemp -d "${work}/XXXXXX")"
    tar xf "${work}/base.tar" -C "${d}"
    printf '{}\n' > "${d}/backup/container/backup.yaml"
    tar cf "${work}/x5r6.tar" -C "${d}" backup
    rm -rf "${d}"

    secreg_import_no_panic "${work}/x5r6.tar"
    secreg_assert_alive

    incus delete -f secreg-x5r6-src 2>/dev/null || true
    rm -rf "${work}"
}

test_security_regression_image_export_traversal() {
    # shellcheck disable=SC2039,3043
    local port work crt key ready target victim conf fp bin server i out

    # GHSA-9pqw-c7m4-xvg7: `incus image export` took the local filename for a
    # downloaded unified image from the server-controlled Content-Disposition header
    # and joined it with the target directory, so a malicious image server could
    # write the export outside the target through a "../"-laden filename. The name
    # is now basenamed before the join.
    port="$(local_tcp_port)"
    work="${TEST_DIR}/secreg-9pqw"
    rm -rf "${work}"
    mkdir -p "${work}"
    crt="${work}/server.crt"
    key="${work}/server.key"
    ready="${work}/ready"
    target="${work}/target"
    conf="${work}/conf"
    # Victim sits one level above the chosen target, on the same filesystem (so the
    # cross-dir rename the exploit performs succeeds; /tmp would be a separate mount).
    victim="${work}/secreg-9pqw-pwned"
    mkdir -p "${target}" "${conf}"
    rm -f "${victim}"

    # Cert needs an IP SAN: the client verifies the hostname even when pinning a
    # self-signed server certificate via --accept-certificate.
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -nodes \
        -keyout "${key}" -out "${crt}" -days 1 -subj "/CN=secreg-9pqw" \
        -addext "subjectAltName=IP:127.0.0.1" >/dev/null 2>&1

    # The body is served as the unified image; its sha256 is requested as the
    # fingerprint so the client's hash check passes and control reaches the rename.
    printf 'secreg-9pqw image body\n' > "${work}/body"
    fp="$(sha256sum "${work}/body" | awk '{print $1}')"

    # Malicious incus-protocol image server: public /1.0, alias lookups 404, and an
    # export response whose Content-Disposition filename escapes the target dir.
    python3 - "${port}" "${crt}" "${key}" "${work}/body" "${ready}" <<'EOF' &
import http.server, ssl, sys, json
port, crt, key, bodyfile, ready = sys.argv[1:6]
body = open(bodyfile, "rb").read()

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, code, ctype, data, extra=None):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        p = self.path.split("?")[0]
        if p == "/1.0":
            meta = {"api_status": "stable", "api_version": "1.0", "auth": "untrusted",
                    "public": True, "auth_methods": ["tls"], "environment": {}}
            self._send(200, "application/json",
                       json.dumps({"type": "sync", "status": "Success",
                                   "status_code": 200, "metadata": meta}).encode())
        elif p.endswith("/export"):
            self._send(200, "application/octet-stream", body, {
                "X-Incus-Type": "incus",
                "Content-Disposition": 'inline; filename="../secreg-9pqw-pwned"',
            })
        else:
            self._send(404, "application/json",
                       b'{"type":"error","error":"not found","error_code":404}')

    do_HEAD = do_GET

httpd = http.server.HTTPServer(("127.0.0.1", int(port)), H)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(crt, key)
httpd.socket = ctx.wrap_socket(httpd.socket, server_side=True)
open(ready, "w").close()
httpd.serve_forever()
EOF
    server=$!

    i=0
    while [ ! -e "${ready}" ] && [ "${i}" -lt 100 ]; do
        sleep 0.1
        i=$((i + 1))
    done

    # Raw binary with an isolated config so the malicious remote and its cert do
    # not touch the test daemon's trust store. --force-local is omitted so the
    # named remote is actually used.
    bin="$(unset -f incus incus_remote 2>/dev/null; command -v incus)"

    out="$(INCUS_CONF="${conf}" "${bin}" remote add secreg-9pqw "https://127.0.0.1:${port}" \
        --protocol incus --public --accept-certificate 2>&1)" || {
        echo "FAIL: could not add the malicious image remote" >&2
        printf '  got: %s\n' "${out}" >&2
        kill "${server}" 2>/dev/null || true
        wait "${server}" 2>/dev/null || true
        rm -rf "${work}"
        false
        return
    }

    out="$(INCUS_CONF="${conf}" "${bin}" image export "secreg-9pqw:${fp}" "${target}" 2>&1 || true)"

    kill "${server}" 2>/dev/null || true
    wait "${server}" 2>/dev/null || true

    # The vulnerable client writes the export to the traversal target; the fixed
    # client basenames it and keeps it inside the chosen directory.
    if [ -e "${victim}" ]; then
        echo "FAIL: image export escaped the target directory to ${victim}" >&2
        printf '  export output: %s\n' "${out}" >&2
        rm -f "${victim}"
        rm -rf "${work}"
        false
        return
    fi

    if [ ! -e "${target}/secreg-9pqw-pwned" ]; then
        echo "FAIL: the export did not complete (basenamed file missing), test inconclusive" >&2
        printf '  export output: %s\n' "${out}" >&2
        rm -rf "${work}"
        false
        return
    fi

    rm -f "${victim}"
    rm -rf "${work}"
}

test_security_regression_bridge_antispoof() {
    # shellcheck disable=SC2039,3043
    local net acl inst dev chain block macpos arppos i

    # GHSA-p7fw-vjjm-2rwp / CVE-2025-52890: a rule-ordering regression in the
    # nftables bridge filter placed the "ct established / arp accept / NDP accept"
    # rules ahead of the MAC anti-spoofing drops, so an instance could ARP/NDP spoof
    # another instance on the same bridge. The accept rules now come after the MAC
    # filter, so a frame with a spoofed source MAC is dropped first.
    if ! command -v nft >/dev/null 2>&1; then
        echo "==> SKIP: nft needed to inspect the bridge firewall"
        export TEST_UNMET_REQUIREMENT="nft needed to inspect the bridge firewall"
        return
    fi

    ensure_import_testimage

    net="secregbr0"
    acl="secregp7fw"
    inst="c-secreg-p7fw"
    dev="nspoof"
    chain="in.${inst}.${dev}"

    incus network create "${net}" ipv4.address=10.42.71.1/24 ipv4.nat=false ipv6.address=none
    incus network acl create "${acl}"
    incus network acl rule add "${acl}" ingress action=drop
    incus network acl rule add "${acl}" egress action=allow

    incus init testimage "${inst}"
    # A bridged NIC with both MAC filtering and an ACL: this renders both the MAC
    # anti-spoof drops and the ACL-gated arp/NDP accept rules in one chain.
    incus config device add "${inst}" "${dev}" nic \
        network="${net}" \
        security.mac_filtering=true \
        security.acls="${acl}"
    incus start "${inst}"

    # Wait for the bridge filter chain to be populated.
    block=""
    i=0
    while [ "${i}" -lt 50 ]; do
        block="$(nft -s list chain bridge incus "${chain}" 2>/dev/null || true)"
        if printf '%s\n' "${block}" | grep -q 'ether saddr != '; then
            break
        fi

        sleep 0.2
        i=$((i + 1))
    done

    if ! printf '%s\n' "${block}" | grep -q 'ether saddr != '; then
        echo "FAIL: MAC anti-spoof rule not found in chain ${chain}" >&2
        printf '  chain: %s\n' "${block}" >&2
        incus delete -f "${inst}" 2>/dev/null || true
        incus network acl delete "${acl}" 2>/dev/null || true
        incus network delete "${net}" 2>/dev/null || true
        false
        return
    fi

    if ! printf '%s\n' "${block}" | grep -q 'ether type arp accept'; then
        echo "FAIL: 'arp accept' rule not found in chain ${chain}" >&2
        printf '  chain: %s\n' "${block}" >&2
        incus delete -f "${inst}" 2>/dev/null || true
        incus network acl delete "${acl}" 2>/dev/null || true
        incus network delete "${net}" 2>/dev/null || true
        false
        return
    fi

    # The MAC anti-spoof drop must come before the blanket arp accept, otherwise a
    # spoofed-MAC ARP frame is accepted before it can be dropped.
    macpos="$(printf '%s\n' "${block}" | grep -n 'ether saddr != ' | head -1 | cut -d: -f1)"
    arppos="$(printf '%s\n' "${block}" | grep -n 'ether type arp accept' | head -1 | cut -d: -f1)"

    if [ "${arppos}" -lt "${macpos}" ]; then
        echo "FAIL: 'arp accept' (line ${arppos}) precedes the MAC anti-spoof drop (line ${macpos}); ARP spoofing is possible" >&2
        printf '  chain: %s\n' "${block}" >&2
        incus delete -f "${inst}" 2>/dev/null || true
        incus network acl delete "${acl}" 2>/dev/null || true
        incus network delete "${net}" 2>/dev/null || true
        false
        return
    fi

    incus delete -f "${inst}"
    incus network acl delete "${acl}"
    incus network delete "${net}"
}

test_security_regression_bridge_dhcp_order() {
    # shellcheck disable=SC2039,3043
    local net acl inst dev chain block macpos dhcppos i

    # GHSA-9q7c-qmhm-jv86 / CVE-2025-52889: a companion ordering regression in the
    # same nftables bridge filter placed the "basic connectivity" accepts (DHCPv4,
    # DNS, IPv6 RS/RA) ahead of the MAC/IP anti-spoof drops, so an instance could
    # send DHCP with a spoofed MAC and exhaust the pool. The DHCP accept now comes
    # after the MAC filter.
    if ! command -v nft >/dev/null 2>&1; then
        echo "==> SKIP: nft needed to inspect the bridge firewall"
        export TEST_UNMET_REQUIREMENT="nft needed to inspect the bridge firewall"
        return
    fi

    ensure_import_testimage

    net="secregbr1"
    acl="secreg9q7c"
    inst="c-secreg-9q7c"
    dev="nspoof"
    chain="in.${inst}.${dev}"

    incus network create "${net}" ipv4.address=10.42.72.1/24 ipv4.nat=false ipv6.address=none
    incus network acl create "${acl}"
    incus network acl rule add "${acl}" ingress action=drop
    incus network acl rule add "${acl}" egress action=allow

    incus init testimage "${inst}"
    incus config device add "${inst}" "${dev}" nic \
        network="${net}" \
        security.mac_filtering=true \
        security.acls="${acl}"
    incus start "${inst}"

    block=""
    i=0
    while [ "${i}" -lt 50 ]; do
        block="$(nft -s list chain bridge incus "${chain}" 2>/dev/null || true)"
        if printf '%s\n' "${block}" | grep -q 'ether saddr != '; then
            break
        fi

        sleep 0.2
        i=$((i + 1))
    done

    if ! printf '%s\n' "${block}" | grep -q 'ether saddr != '; then
        echo "FAIL: MAC anti-spoof rule not found in chain ${chain}" >&2
        printf '  chain: %s\n' "${block}" >&2
        incus delete -f "${inst}" 2>/dev/null || true
        incus network acl delete "${acl}" 2>/dev/null || true
        incus network delete "${net}" 2>/dev/null || true
        false
        return
    fi

    if ! printf '%s\n' "${block}" | grep -q 'udp dport 67 accept'; then
        echo "FAIL: DHCPv4 accept rule not found in chain ${chain}" >&2
        printf '  chain: %s\n' "${block}" >&2
        incus delete -f "${inst}" 2>/dev/null || true
        incus network acl delete "${acl}" 2>/dev/null || true
        incus network delete "${net}" 2>/dev/null || true
        false
        return
    fi

    # The MAC anti-spoof drop must come before the DHCP accept, otherwise a
    # spoofed-MAC DHCP request is accepted before it can be dropped.
    macpos="$(printf '%s\n' "${block}" | grep -n 'ether saddr != ' | head -1 | cut -d: -f1)"
    dhcppos="$(printf '%s\n' "${block}" | grep -n 'udp dport 67 accept' | head -1 | cut -d: -f1)"

    if [ "${dhcppos}" -lt "${macpos}" ]; then
        echo "FAIL: DHCP accept (line ${dhcppos}) precedes the MAC anti-spoof drop (line ${macpos}); DHCP spoofing is possible" >&2
        printf '  chain: %s\n' "${block}" >&2
        incus delete -f "${inst}" 2>/dev/null || true
        incus network acl delete "${acl}" 2>/dev/null || true
        incus network delete "${net}" 2>/dev/null || true
        false
        return
    fi

    incus delete -f "${inst}"
    incus network acl delete "${acl}"
    incus network delete "${net}"
}

test_security_regression_webui_token() {
    # shellcheck disable=SC2039,3043
    local uidir out bin url port token webpid code goodcode i

    # GHSA-453r-g2pg-cxxq / CVE-2026-33898: the local web server started by
    # `incus webui` validated the auth_token only when it arrived in a cookie; a
    # token supplied in the URL query was persisted as a cookie and accepted without
    # being compared to the expected value. A local or remote attacker who reached
    # the random localhost port therefore gained full access. The URL token is now
    # validated too (and an Origin check was added).
    uidir="${TEST_DIR}/secreg-ui"
    out="${TEST_DIR}/secreg-webui.out"
    mkdir -p "${uidir}"
    echo '<html>secreg-ui</html>' > "${uidir}/index.html"
    rm -f "${out}"

    # webui refuses to start unless the daemon serves /ui/, which requires INCUS_UI
    # set when the daemon starts, so restart the daemon with it exported.
    export INCUS_UI="${uidir}"
    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true

    bin="$(unset -f incus incus_remote 2>/dev/null; command -v incus)"

    # webui blocks serving, so run it in the background and read its announced URL.
    "${bin}" webui > "${out}" 2>&1 &
    webpid=$!

    url=""
    i=0
    while [ "${i}" -lt 50 ]; do
        url="$(grep -o 'http://127.0.0.1:[0-9]*/ui?auth_token=[0-9a-f-]*' "${out}" 2>/dev/null | head -1 || true)"
        if [ -n "${url}" ]; then
            break
        fi

        sleep 0.2
        i=$((i + 1))
    done

    port="$(printf '%s' "${url}" | sed -n 's|http://127.0.0.1:\([0-9]*\)/.*|\1|p')"
    token="$(printf '%s' "${url}" | sed -n 's|.*auth_token=\(.*\)|\1|p')"

    goodcode=""
    code=""
    if [ -n "${port}" ] && [ -n "${token}" ]; then
        # The genuine token must be accepted (sanity, so a bogus rejection is real).
        goodcode="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/1.0?auth_token=${token}" || true)"

        # A bogus token in the URL must be rejected.
        code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/1.0?auth_token=00000000-0000-0000-0000-000000000000" || true)"
    fi

    kill "${webpid}" 2>/dev/null || true
    wait "${webpid}" 2>/dev/null || true

    # Restore the daemon to its normal (no-UI) state for later tests.
    unset INCUS_UI
    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true

    if [ -z "${url}" ]; then
        echo "FAIL: incus webui did not announce a web server URL" >&2
        cat "${out}" >&2 2>/dev/null || true
        rm -f "${out}"
        false
        return
    fi

    if [ "${goodcode}" = "401" ] || [ -z "${goodcode}" ]; then
        echo "FAIL: the genuine auth_token was rejected (code '${goodcode}'); test inconclusive" >&2
        rm -f "${out}"
        false
        return
    fi

    if [ "${code}" != "401" ]; then
        echo "FAIL: a bogus auth_token in the URL was accepted (HTTP '${code}'); webui authentication bypass" >&2
        rm -f "${out}"
        false
        return
    fi

    rm -f "${out}"
}

test_security_regression_simplestreams_fingerprint() {
    # shellcheck disable=SC2039,3043
    local work srv build crt key ready conf port bin real fake out server i

    # GHSA-p8mm-23gg-jc9r / CVE-2026-33542: the simplestreams client verified each
    # downloaded file against the per-file sha256 from the (server-controlled) index
    # but never checked that the combined image hash matched the requested
    # fingerprint, so a malicious server could serve content that did not correspond
    # to the fingerprint the user asked for. The full image hash is now validated.
    if ! command -v incus-simplestreams >/dev/null 2>&1; then
        echo "==> SKIP: incus-simplestreams needed to build the catalog"
        export TEST_UNMET_REQUIREMENT="incus-simplestreams needed to build the catalog"
        return
    fi

    work="${TEST_DIR}/secreg-p8mm"
    srv="${work}/srv"
    build="${work}/build"
    conf="${work}/conf"
    crt="${work}/s.crt"
    key="${work}/s.key"
    ready="${work}/ready"
    rm -rf "${work}"
    mkdir -p "${srv}" "${build}" "${conf}/servercerts" "${work}/out"

    # Build a minimal but valid simplestreams catalog (no daemon needed).
    cat > "${build}/metadata.yaml" <<YAML
architecture: x86_64
creation_date: 1700000000
properties:
  description: secreg p8mm
  os: secreg
  release: test
  variant: default
  architecture: x86_64
YAML
    tar cJf "${build}/meta.tar.xz" -C "${build}" metadata.yaml
    mkdir -p "${build}/rootfs-dir"
    echo secreg-p8mm > "${build}/rootfs-dir/file"
    tar cJf "${build}/rootfs.tar.xz" -C "${build}/rootfs-dir" .

    ( cd "${srv}" && incus-simplestreams add "${build}/meta.tar.xz" "${build}/rootfs.tar.xz" >/dev/null 2>&1 )

    # The advertised combined fingerprint; tamper it so it no longer matches the
    # actual hash of the served files, leaving per-file hashes (and paths) intact.
    real="$(grep -oE 'combined_rootxz_sha256":"[0-9a-f]{64}' "${srv}/streams/v1/images.json" | head -1 | grep -oE '[0-9a-f]{64}')"
    fake="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    if [ -z "${real}" ]; then
        echo "FAIL: could not read the catalog's combined fingerprint" >&2
        rm -rf "${work}"
        false
        return
    fi

    python3 - "${srv}/streams/v1/images.json" "${real}" "${fake}" <<'EOF'
import sys, pathlib
p = pathlib.Path(sys.argv[1])
old = '"combined_rootxz_sha256":"' + sys.argv[2] + '"'
new = '"combined_rootxz_sha256":"' + sys.argv[3] + '"'
s = p.read_text()
p.write_text(s.replace(old, new, 1))
EOF

    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -nodes \
        -keyout "${key}" -out "${crt}" -days 1 -subj "/CN=secreg-p8mm" \
        -addext "subjectAltName=IP:127.0.0.1" >/dev/null 2>&1

    port="$(local_tcp_port)"

    # Static server answering both plaintext HTTP and TLS on one port: the index is
    # fetched over HTTPS, the image files over HTTP (the client tries HTTP first).
    python3 - "${srv}" "${port}" "${crt}" "${key}" "${ready}" <<'EOF' &
import socket, ssl, sys, http.server, socketserver, functools
root, port, crt, key, ready = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(crt, key)

class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass

Handler = functools.partial(H, directory=root)

class Dual(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

    def get_request(self):
        sock, addr = self.socket.accept()
        try:
            peek = sock.recv(1, socket.MSG_PEEK)
        except Exception:
            peek = b''
        if peek == b'\x16':
            sock = ctx.wrap_socket(sock, server_side=True)
        return sock, addr

    def handle_error(self, request, client_address):
        pass

srv = Dual(("127.0.0.1", port), Handler)
open(ready, "w").close()
srv.serve_forever()
EOF
    server=$!

    i=0
    while [ ! -e "${ready}" ] && [ "${i}" -lt 100 ]; do
        sleep 0.1
        i=$((i + 1))
    done

    bin="$(unset -f incus incus_remote 2>/dev/null; command -v incus)"
    INCUS_CONF="${conf}" "${bin}" remote add secreg-p8mm "https://127.0.0.1:${port}" \
        --protocol simplestreams --accept-certificate >/dev/null 2>&1
    # Pin the self-signed cert (remote add does not do this for simplestreams).
    cp "${crt}" "${conf}/servercerts/secreg-p8mm.crt"

    out="$(INCUS_CONF="${conf}" "${bin}" image export "secreg-p8mm:${fake}" "${work}/out/" 2>&1 || true)"

    kill "${server}" 2>/dev/null || true
    wait "${server}" 2>/dev/null || true

    if ! printf '%s\n' "${out}" | grep -qF "Image fingerprint doesn't match"; then
        echo "FAIL: a simplestreams image whose content didn't match the requested fingerprint was accepted" >&2
        printf '  export output: %s\n' "${out}" >&2
        rm -rf "${work}"
        false
        return
    fi

    rm -rf "${work}"
}

test_security_regression_ovn_tls_ca() {
    # GHSA-c839-4qxr-j4x3 / CVE-2026-40243: the OVN database TLS clients disabled
    # Go's server verification and used a custom verifier that built its trust
    # roots from the certificates the peer presented, so any peer certificate was
    # accepted. The fix anchors trust in the configured CA. Exercised on the
    # northbound client via a rogue server whose cert is signed by a foreign CA.

    # Needs OVS: the daemon reads the southbound address from external_ids before
    # connecting to the northbound DB, so without it setupOVN never reaches the
    # TLS code.
    if ! command -v ovs-vsctl >/dev/null 2>&1 || ! ovs-vsctl show >/dev/null 2>&1; then
        echo "==> SKIP: Open vSwitch needed to reach the OVN client"
        export TEST_UNMET_REQUIREMENT="Open vSwitch needed to reach the OVN client"
        return
    fi

    # shellcheck disable=SC2039,3043
    local sock work port out ready server set_remote i

    sock="${INCUS_DIR}/unix.socket"

    # A southbound remote must be set for setupOVN to proceed; set a dummy one if
    # the host has none, and remove it again afterwards.
    set_remote=0
    if [ -z "$(ovs-vsctl --if-exists get open_vswitch . external_ids:ovn-remote 2>/dev/null | tr -d '"')" ]; then
        ovs-vsctl set open_vswitch . external_ids:ovn-remote="ssl:127.0.0.1:16642"
        set_remote=1
    fi

    work="$(mktemp -d -p "${TEST_DIR}" XXX)"
    (
        cd "${work}" || exit 1
        # Configured (trusted) CA and the client certificate it signs.
        openssl req -x509 -newkey rsa:2048 -nodes -keyout goodca.key -out goodca.crt -days 3650 -subj "/CN=secreg-good-ca" >/dev/null 2>&1
        openssl req -newkey rsa:2048 -nodes -keyout client.key -out client.csr -subj "/CN=secreg-client" >/dev/null 2>&1
        openssl x509 -req -in client.csr -CA goodca.crt -CAkey goodca.key -CAcreateserial -out client.crt -days 3650 >/dev/null 2>&1
        # A foreign CA and the rogue server certificate it signs.
        openssl req -x509 -newkey rsa:2048 -nodes -keyout attackerca.key -out attackerca.crt -days 3650 -subj "/CN=secreg-attacker-ca" >/dev/null 2>&1
        openssl req -newkey rsa:2048 -nodes -keyout server.key -out server.csr -subj "/CN=secreg-rogue-nb" >/dev/null 2>&1
        openssl x509 -req -in server.csr -CA attackerca.crt -CAkey attackerca.key -CAcreateserial -out server.crt -days 3650 >/dev/null 2>&1
        # The rogue server offers its foreign CA alongside the leaf, which the
        # broken verifier would have trusted as a root.
        cat server.crt attackerca.crt > serverchain.pem
    )

    port="$(local_tcp_port)"
    ready="${work}/ready"
    python3 - "${port}" "${work}/serverchain.pem" "${work}/server.key" "${ready}" <<'EOF' &
import socket, ssl, sys, threading
port, chain, key, ready = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(chain, key)
ctx.verify_mode = ssl.CERT_NONE
srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", port))
srv.listen(5)
open(ready, "w").close()

def handle(c):
    try:
        ss = ctx.wrap_socket(c, server_side=True)
        ss.recv(4096)
    except Exception:
        pass
    finally:
        try:
            c.close()
        except Exception:
            pass

while True:
    try:
        conn, _ = srv.accept()
    except Exception:
        break
    threading.Thread(target=handle, args=(conn,), daemon=True).start()
EOF
    server=$!

    i=0
    while [ ! -e "${ready}" ] && [ "${i}" -lt 100 ]; do
        sleep 0.1
        i=$((i + 1))
    done

    # Point the northbound client at the rogue server with the trusted CA
    # configured; setting the keys triggers an immediate connection attempt.
    out="$(curl -k -s --unix-socket "${sock}" -X PATCH \
        --data-binary "$(jq -n --arg nb "ssl:127.0.0.1:${port}" --arg ca "$(cat "${work}/goodca.crt")" --arg cc "$(cat "${work}/client.crt")" --arg ck "$(cat "${work}/client.key")" '{config:{"network.ovn.northbound_connection":$nb,"network.ovn.ca_cert":$ca,"network.ovn.client_cert":$cc,"network.ovn.client_key":$ck}}')" \
        incus/1.0 2>&1)"

    kill "${server}" 2>/dev/null || true
    wait "${server}" 2>/dev/null || true

    # Clear any config that might have been stored.
    curl -k -s --unix-socket "${sock}" -X PATCH \
        -d '{"config":{"network.ovn.northbound_connection":"","network.ovn.ca_cert":"","network.ovn.client_cert":"","network.ovn.client_key":""}}' \
        incus/1.0 >/dev/null 2>&1 || true

    if [ "${set_remote}" = 1 ]; then
        ovs-vsctl remove open_vswitch . external_ids ovn-remote >/dev/null 2>&1 || true
    fi
    rm -rf "${work}"

    if ! printf '%s\n' "${out}" | grep -qi "certificate signed by unknown authority"; then
        echo "FAIL: OVN northbound client accepted a server certificate not signed by the configured CA" >&2
        printf '  patch response: %s\n' "${out}" >&2
        false
        return
    fi

    secreg_assert_alive
}

test_security_regression_cluster_migration_override() {
    # GHSA-qw5c-v953-38gw / CVE-2026-62940: the cross-member migration handler
    # applied user-supplied config, device and profile overrides without any
    # project restriction check, so a restricted project could set keys such as
    # security.privileged while moving an instance between cluster members. The
    # fix re-checks the overridden config against the project's restrictions.

    # Needs the clustering network infrastructure (bridge + netns).
    if ! command -v iptables >/dev/null 2>&1; then
        echo "==> SKIP: iptables needed for the clustering bridge"
        export TEST_UNMET_REQUIREMENT="iptables needed for the clustering bridge"
        return
    fi

    # shellcheck disable=SC2039,3043
    local INCUS_DIR INCUS_ONE_DIR INCUS_TWO_DIR ns1 ns2 prefix bridge cert out rc priv d

    setup_clustering_bridge
    prefix="sqw$$"
    bridge="${prefix}"

    setup_clustering_netns 1
    INCUS_ONE_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
    chmod +x "${INCUS_ONE_DIR}"
    ns1="${prefix}1"
    spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

    cert="$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")"

    setup_clustering_netns 2
    INCUS_TWO_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
    chmod +x "${INCUS_TWO_DIR}"
    ns2="${prefix}2"
    spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

    INCUS_DIR="${INCUS_ONE_DIR}" ensure_import_testimage

    # A restricted project that forbids privileged containers; cluster targeting is
    # allowed so the move isn't blocked for an unrelated reason.
    INCUS_DIR="${INCUS_ONE_DIR}" incus project create secreg-qw5c \
        -c restricted=true \
        -c restricted.cluster.target=allow \
        -c features.images=false
    INCUS_DIR="${INCUS_ONE_DIR}" incus profile device add default root disk pool=data path=/ --project secreg-qw5c

    INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage secreg-qw5c-c1 --project secreg-qw5c --target node1

    # Try to gain privilege via a config override on a cross-member move.
    out="$(INCUS_DIR="${INCUS_ONE_DIR}" incus move secreg-qw5c-c1 --project secreg-qw5c --target node2 --config security.privileged=true 2>&1)" && rc=0 || rc=1

    # Record the resulting privilege level before tearing the cluster down.
    priv="$(INCUS_DIR="${INCUS_ONE_DIR}" incus config get secreg-qw5c-c1 security.privileged --project secreg-qw5c 2>/dev/null || true)"

    INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f secreg-qw5c-c1 --project secreg-qw5c 2>/dev/null || true
    INCUS_DIR="${INCUS_ONE_DIR}" incus profile device remove default root --project secreg-qw5c 2>/dev/null || true
    INCUS_DIR="${INCUS_ONE_DIR}" incus project delete secreg-qw5c 2>/dev/null || true

    # Hard-kill both members and drop them from cleanup tracking. Graceful
    # teardown of a quorum-less two-node cluster is far too slow, and the final
    # wipe of TEST_DIR removes the leftover directories.
    for d in "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"; do
        if [ -e "${d}/incus.pid" ]; then
            kill -9 "$(cat "${d}/incus.pid")" 2>/dev/null || true
        fi
        find "${d}" -name shmounts -exec umount -l {} \; 2>/dev/null || true
        find "${d}" -name dev_incus -exec umount -l {} \; 2>/dev/null || true
        sed "\\|^${d}|d" -i "${TEST_DIR}/daemons" 2>/dev/null || true
    done
    teardown_clustering_netns || true
    teardown_clustering_bridge || true

    if [ "${rc}" = 0 ] || [ "${priv}" = "true" ]; then
        echo "FAIL: cross-member migration applied a privileged override in a restricted project" >&2
        printf '  move rc=%s priv=%s output: %s\n' "${rc}" "${priv}" "${out}" >&2
        false
        return
    fi

    if ! printf '%s\n' "${out}" | grep -qiE "privileged|forbidden|restricted"; then
        echo "FAIL: cross-member migration override was rejected but not for a restriction reason" >&2
        printf '  move output: %s\n' "${out}" >&2
        false
        return
    fi
}

test_security_regression_cluster_refresh_pull() {
    # GHSA-443p-7392-h4v2 / CVE-2026-115370: on a clustered server a migration
    # request with refresh set for an existing instance short-circuits straight
    # into the migration sink, skipping the AllowInstanceCreation check that
    # forbids pull mode migration in restricted projects. A restricted project
    # could thus make the server open an outbound connection to an arbitrary URL.
    # The pull mode restriction is now checked at the start of that short-circuit.

    # Needs the clustering network infrastructure (bridge + netns).
    if ! command -v iptables >/dev/null 2>&1; then
        echo "==> SKIP: iptables needed for the clustering bridge"
        export TEST_UNMET_REQUIREMENT="iptables needed for the clustering bridge"
        return
    fi

    # shellcheck disable=SC2039,3043
    local INCUS_DIR INCUS_ONE_DIR INCUS_TWO_DIR ns1 ns2 prefix bridge cert out d sock

    setup_clustering_bridge
    prefix="s4p$$"
    bridge="${prefix}"

    setup_clustering_netns 1
    INCUS_ONE_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
    chmod +x "${INCUS_ONE_DIR}"
    ns1="${prefix}1"
    spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

    cert="$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")"

    setup_clustering_netns 2
    INCUS_TWO_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
    chmod +x "${INCUS_TWO_DIR}"
    ns2="${prefix}2"
    spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

    INCUS_DIR="${INCUS_ONE_DIR}" ensure_import_testimage

    INCUS_DIR="${INCUS_ONE_DIR}" incus project create secreg-443p \
        -c restricted=true \
        -c features.images=false
    INCUS_DIR="${INCUS_ONE_DIR}" incus profile device add default root disk pool=data path=/ --project secreg-443p

    # An existing instance so the refresh short-circuit is taken.
    INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage secreg-443p-c1 --project secreg-443p --target node1

    # A pull mode migration refresh. The dead peer URL fails fast if the check is
    # skipped, so the restriction rejection is the only "pull mode" signal.
    sock="${INCUS_ONE_DIR}/unix.socket"
    out="$(curl -k -s --unix-socket "${sock}" -X POST \
        -d '{"name":"secreg-443p-c1","source":{"type":"migration","mode":"pull","refresh":true,"operation":"https://127.0.0.1:1/1.0/operations/00000000-0000-0000-0000-000000000000/websocket","secrets":{"control":"secreg"}}}' \
        "incus/1.0/instances?project=secreg-443p" 2>&1)"

    INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f secreg-443p-c1 --project secreg-443p 2>/dev/null || true
    INCUS_DIR="${INCUS_ONE_DIR}" incus profile device remove default root --project secreg-443p 2>/dev/null || true
    INCUS_DIR="${INCUS_ONE_DIR}" incus project delete secreg-443p 2>/dev/null || true

    for d in "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"; do
        if [ -e "${d}/incus.pid" ]; then
            kill -9 "$(cat "${d}/incus.pid")" 2>/dev/null || true
        fi
        find "${d}" -name shmounts -exec umount -l {} \; 2>/dev/null || true
        find "${d}" -name dev_incus -exec umount -l {} \; 2>/dev/null || true
        sed "\\|^${d}|d" -i "${TEST_DIR}/daemons" 2>/dev/null || true
    done
    teardown_clustering_netns || true
    teardown_clustering_bridge || true

    if ! printf '%s\n' "${out}" | grep -qF "Restricted projects aren't allowed to use pull mode migration"; then
        echo "FAIL: clustered refresh allowed pull mode migration in a restricted project" >&2
        printf '  response: %s\n' "${out}" >&2
        false
        return
    fi
}
