test_nvram_vm() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    incus network create incusbr0
    incus profile device remove default eth0
    incus profile device add default eth0 nic network=incusbr0 name=eth0

    poolName="vmpool$$"

    echo "==> Create storage pool"
    incus storage create "${poolName}" dir

    echo "==> Create VM"
    incus init images:debian/13 v1 --vm -s "${poolName}"

    # Update the NVRAM.
    printf "\7\0" | incus low-level nvram set v1 Timeout=- --format=binary
    echo '{"data":20,"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"]}' | incus low-level nvram set v1 MTC:MTC=- --format=json
    incus low-level nvram set v1 Foo=bar --format=binary
    incus low-level nvram set v1 00112233-4455-6677-8899-aabbccddeeff:Foo=baz --format=binary
    incus low-level nvram set v1 FooBar=baz --format=binary --attributes=3

    # Explore the NVRAM.
    [ "$(incus low-level nvram get v1 Boot0000 --format=json | jq -r .data.paths.[0].[0])" = "Fv(7cb8bdc9-f8eb-4f34-aaea-3ee4af6516a1)/FvFile(462caa21-7614-4503-836e-8ab6f4662331)" ]
    printf '\7\0\0\0\7\0' | { exec 3<&0; incus low-level nvram get v1 Timeout --format=efivarfs | cmp -s - /dev/fd/3; }
    [ "$(incus low-level nvram get v1 MTC:MTC --format=base64)" = "FAAAAA==" ]
    [ "$(incus low-level nvram get v1 Foo --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"],"binary":"YmFy"}' ]
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:Foo --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"],"binary":"YmF6"}' ]
    [ "$(incus low-level nvram get v1 FooBar --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS"],"binary":"YmF6"}' ]

    # Bulk-update the NVRAM.
    echo '{"data":6,"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"]}' | incus low-level nvram set v1 Timeout=- FooBar= --format=json
    ! incus low-level nvram get v1 FooBar || false
    [ "$(incus low-level nvram get v1 Timeout --format=hex)" = "0600" ]

    # Check that the OS boots and our NVRAM changes survive it.
    incus start v1
    incus wait v1 agent
    incus stop v1
    [ "$(incus low-level nvram get v1 Boot0000 --format=json | jq -r .data.paths.[0].[0])" = "Fv(7cb8bdc9-f8eb-4f34-aaea-3ee4af6516a1)/FvFile(462caa21-7614-4503-836e-8ab6f4662331)" ]
    # We don’t re-check Timeout and MTC as the firmware surely has messed with them.
    [ "$(incus low-level nvram get v1 Foo --format=binary)" = "bar" ]
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:Foo --format=binary)" = "baz" ]

    # Break and repair Secure Boot.
    incus low-level secureboot list v1 db --columns=fs --format=csv | grep -i "microsoft.*uefi" | cut -d, -f1 | xargs incus low-level secureboot remove v1 db
    incus start v1
    sleep 5
    incus console --show-log v1 | grep "Access Denied"
    incus stop -f v1
    curl -L "https://go.microsoft.com/fwlink/?linkid=2239872" | incus low-level secureboot add v1 db - --owner=microsoft
    PK="$(incus low-level secureboot export v1 pk -)"
    ! echo "$PK" | incus low-level secureboot add v1 pk - || false
    echo "$PK" | incus low-level secureboot add v1 pk - --skip
    incus start v1
    sleep 5
    ! incus console --show-log v1 | grep "Access Denied" || false
    incus wait v1 agent
    incus stop v1

    # Force our way into and out of the setup menu.
    incus start v1 --override-boot=0
    sleep 10
    incus console --show-log v1 | grep "Boot Maintenance Manager"
    { sleep 3; printf "\033[A\033[A\r"; } | timeout -p 5 script -efc "incus console v1" /dev/null
    if [ -t 0 ]; then
        stty sane
        tput sgr0
        tput cnorm
    fi
    incus wait v1 agent
    incus stop v1

    # Use a scriptlet.
    incus config set v1 raw.qemu.scriptlet=- <<EOF
def qemu_hook(instance, stage):
  if stage == "config":
    set_nvram_var("OVMF_PLATFORM_CONFIG_GUID", "PlatformConfig", {
      "data": {"width": 1280, "height": 768},
      "attributes": ["NON_VOLATILE", "BOOTSERVICE_ACCESS", "RUNTIME_ACCESS"]
    })
    set_raw_nvram_var("00112233-4455-6677-8899-aabbccddeeff", "abc", b"def", attributes=3)
    if "Lang" not in list_nvram_vars()["8be4df61-93ca-11d2-aa0d-00e098032b8c"]:
      fail("lang")
    if "PlatformLang" not in list_nvram_vars("EFI_GLOBAL_VARIABLE"):
      fail("platformLang")
    unset_nvram_var("EFI_GLOBAL_VARIABLE", "Lang")
  else:
    if get_raw_nvram_var("OVMF_PLATFORM_CONFIG_GUID", "PlatformConfig") != b"\x00\x05\x00\x00\x00\x03\x00\x00":
      fail("platformConfig")
    platformConfig = get_nvram_var("OVMF_PLATFORM_CONFIG_GUID", "PlatformConfig")
    if platformConfig.data.width != 1280:
      fail("platformConfig.data.width")
    if platformConfig.data.height != 768:
      fail("platformConfig.data.height")
    if platformConfig.attributes != ["NON_VOLATILE", "BOOTSERVICE_ACCESS", "RUNTIME_ACCESS"]:
      fail("platformConfig.attributes")
    if platformConfig.timestamp != None:
      fail("platformConfig.timestamp")
    if platformConfig.binary != b"\x00\x05\x00\x00\x00\x03\x00\x00":
      fail("platformConfig.binary")
    if not has_nvram_var("00112233-4455-6677-8899-aabbccddeeff", "abc"):
      fail("abc")
    abc = get_nvram_var("00112233-4455-6677-8899-aabbccddeeff", "abc")
    if abc.data != None:
      fail("abc.data")
    if abc.attributes != ["NON_VOLATILE", "BOOTSERVICE_ACCESS"]:
      fail("abc.attributes")
    if abc.timestamp != None:
      fail("abc.timestamp")
    if abc.binary != b"def":
      fail("abc.binary")
EOF
    incus start v1
    incus wait v1 agent
    incus stop v1
    incus config unset v1 raw.qemu.scriptlet
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:abc --format=binary)" = "def" ]

    # Use configuration overrides.
    incus config set v1 initial.nvram-binary.00112233-4455-6677-8899-aabbccddeeff.Foo=QmFy
    incus config set v1 initial.nvram-binary.00112233-4455-6677-8899-aabbccddeeff.Baz=3:UXV4
    echo '{"data":{"width": 800,"height":600},"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"]}' | incus config set v1 initial.nvram.7235c51c-0c80-4cab-87ac-3b084a6304b1.PlatformConfig=-
    incus config set v1 initial.secureboot.db=- <<EOF
-----BEGIN CERTIFICATE-----
MIIFpDCCA4ygAwIBAgITMwAAABY2vzaJnxV1zAAAAAAAFjANBgkqhkiG9w0BAQsF
ADBaMQswCQYDVQQGEwJVUzEeMBwGA1UEChMVTWljcm9zb2Z0IENvcnBvcmF0aW9u
MSswKQYDVQQDEyJNaWNyb3NvZnQgUlNBIERldmljZXMgUm9vdCBDQSAyMDIxMB4X
DTIzMDYxMzE5MjE0N1oXDTM4MDYxMzE5MzE0N1owTjELMAkGA1UEBhMCVVMxHjAc
BgNVBAoTFU1pY3Jvc29mdCBDb3Jwb3JhdGlvbjEfMB0GA1UEAxMWTWljcm9zb2Z0
IFVFRkkgQ0EgMjAyMzCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAL0i
Kq7vGjGFE3hRp5v9/HjRY7gam2P1EgbbS0E1am+r9WoEzJfPu9QICRphOg3ms6BG
/wmt3oAk3BKA8l/ZFu3iQp3NL01hAmGKHEsdGGI5hpdxrT5/XXETS+kqAMG+1bcA
n15lsiwa/3Tt6oPSOYkzNXN9oKL6QORmUFiq/IfoXCCDNOyr4gvFXz7/SCsRkSbv
GG5XxZ8Yc5nv4Wp0K7svf1COHdo9drYE5cwuEMeDG4Oj5KUTE3FuM3ijqDzsSCZe
x8ZeDYeaqsxVNIGtnZD15pZjpugHIBfIkx7SrqTcrn1Zv4heYgyuW/IpQFYdJkDe
haatVtHPVUd2X5w52wMCAwEAAaOCAW0wggFpMA4GA1UdDwEB/wQEAwIBhjAQBgkr
BgEEAYI3FQEEAwIBADAdBgNVHQ4EFgQUgaprMkTJNbzg1mKK85gnQh4ySX0wGQYJ
KwYBBAGCNxQCBAweCgBTAHUAYgBDAEEwDwYDVR0TAQH/BAUwAwEB/zAfBgNVHSME
GDAWgBSERIYGAJg/LKqzxYnzrC7J5p0JAzBlBgNVHR8EXjBcMFqgWKBWhlRodHRw
Oi8vd3d3Lm1pY3Jvc29mdC5jb20vcGtpb3BzL2NybC9NaWNyb3NvZnQlMjBSU0El
MjBEZXZpY2VzJTIwUm9vdCUyMENBJTIwMjAyMS5jcmwwcgYIKwYBBQUHAQEEZjBk
MGIGCCsGAQUFBzAChlZodHRwOi8vd3d3Lm1pY3Jvc29mdC5jb20vcGtpb3BzL2Nl
cnRzL01pY3Jvc29mdCUyMFJTQSUyMERldmljZXMlMjBSb290JTIwQ0ElMjAyMDIx
LmNydDANBgkqhkiG9w0BAQsFAAOCAgEAB2ATKlOHEg8a81oUlRfl2NeVVJuLDt2R
pe3HXUdQk0W3lYhfFxlBY3a1grCoxZ2ZFTaJSb4Swmb7gwywgc7lpKvCoJrr9Qc8
/iH4mtwZIQyeJCzRXKIWCkvr7EicsVt02wFkwuOAaqsazXcbajmat7pwRP9nlMWB
BvDLgQSTJyGZvYeIFJwicQ4LL1y+uJBUfMAevCubo1YXS5fn438TNPqwNGub9rIt
99h72CDTXKeVTE8q+eceaK/8bI/Ihj2fyNHvTRrI0fb9LXzj6EHB6ifB+44lhlqJ
phC+zuOPpXvEGqDodZD9IbDBo8UWI148zi/+jJi/CFz2ucWyPLbMyOx/0nd0y+3z
lsmLjRwqiQ+jj73OKoVGmiOij0LAmdbqhR9hGb4WNbd1oJWAZQaH1As1yMSqDs6i
CmNgyksrXCcEgq8+WIN6WthnPxBT9QwW9yZLioC5xR+g3tjTYUQURaf1q5qIF/23
lFQCi+S3U6E+jZ5QgqgA4HiUG76zxDAfsg7b8EaQweZX/nzBcLIcS2TZEAMbNPtm
z4JunkCoETfyZYshCa88k2I987yD3T9VkBXSMa8R5/jKoILhuc+zV5PHVTesf0G/
H5Y88yaU+djSVSSKirZB8OAWwCOSjHEKTGoNGVX3OpySIZah1fgKjJ2/yevKiEL8
S7Tv/ycwIWE=
-----END CERTIFICATE-----
-----BEGIN SIGNATURE-----
AAARESIiMzNERFVVZmZ3d4iImZmqqru7zMzd3e7u//8=
-----END SIGNATURE-----
EOF
    incus config set v1 volatile.apply_nvram=true
    incus start v1
    incus wait v1 agent
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:Foo --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"],"binary":"QmFy"}' ]
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:Baz --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS"],"binary":"UXV4"}' ]
    [ "$(incus low-level nvram get v1 OVMF_PLATFORM_CONFIG_GUID:PlatformConfig --format=json)" = '{"data":{"height":600,"width":800},"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"],"binary":"IAMAAFgCAAA="}' ]
    [ "$(incus low-level secureboot list v1 db --format=csv --columns=tf | tr '\n' :)" = "sha256,000011112222:x509,f6124e34125b:" ]
    incus stop v1
    DB="$(incus low-level secureboot export v1 db -)"
    incus low-level secureboot remove v1 db 000011112222
    incus low-level secureboot remove v1 db f6124e34125b
    echo "$DB" | incus low-level secureboot add v1 db -
    [ "$(incus low-level secureboot list v1 db --format=csv --columns=tf | tr '\n' :)" = "x509,f6124e34125b:" ]
    echo "$DB" | incus low-level secureboot add v1 db - --skip --bundle
    [ "$(incus low-level secureboot list v1 db --format=csv --columns=tf | tr '\n' :)" = "sha256,000011112222:x509,f6124e34125b:" ]
    incus low-level nvram unset v1 EFI_IMAGE_SECURITY_DATABASE_GUID:db
    echo "$DB" | incus low-level secureboot import v1 db -
    [ "$(incus low-level secureboot export v1 db -)" = "$DB" ]
    ! echo "$DB" | incus low-level secureboot import v1 db - || false
    echo "$DB" | incus low-level secureboot import v1 db - --force

    echo "==> Deleting VM"
    incus rm -f v1

    echo "==> Deleting storage pool"
    incus storage delete "${poolName}"

    echo "==> Restoring profile and deleting network"
    incus profile device remove default eth0
    incus profile device add default eth0 nic nictype=p2p name=eth0
    incus network delete incusbr0
}
