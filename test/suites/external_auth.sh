test_macaroon_auth() {
    # shellcheck disable=SC2039,3043
    local identity_endpoint
    # shellcheck disable=SC2086
    identity_endpoint="$(cat ${TEST_DIR}/macaroon-identity.endpoint)"

    ensure_has_localhost_remote "$INCUS_ADDR"

    inc config set candid.api.url "$identity_endpoint"
    key=$(curl -s "${identity_endpoint}/discharge/info" | jq .PublicKey)
    inc config set candid.api.key "${key}"

    # invalid credentials make the remote add fail
    ! (
    cat <<EOF
wrong-user
wrong-pass
EOF
    ) | inc remote add macaroon-remote "https://$INCUS_ADDR" --auth-type candid --accept-certificate || false

    # valid credentials work
    (
    cat <<EOF
user1
pass1
EOF
    ) | inc remote add macaroon-remote "https://$INCUS_ADDR" --auth-type candid --accept-certificate

    # run a inc command through the new remote
    inc config show macaroon-remote: | grep -q candid.api.url

    # cleanup
    inc config unset candid.api.url
    inc config unset core.https_address
    inc remote remove macaroon-remote
}
