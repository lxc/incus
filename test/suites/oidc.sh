test_oidc() {
    ensure_import_testimage

    # shellcheck disable=2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # Setup OIDC
    spawn_oidc
    incus config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
    incus config set "oidc.client.id=device"

    BROWSER=curl incus remote add --accept-certificate oidc "${INCUS_ADDR}" --auth-type oidc
    [ "$(incus info oidc: | grep ^auth_user_name | sed "s/.*: //g")" = "unknown" ]
    incus remote remove oidc

    set_oidc test-user

    BROWSER=curl incus remote add --accept-certificate oidc "${INCUS_ADDR}" --auth-type oidc
    [ "$(incus info oidc: | grep ^auth_user_name | sed "s/.*: //g")" = "test-user" ]
    incus remote remove oidc

    # Opaque access tokens are validated through the ID token
    incus config set "oidc.client.id=device-opaque"
    BROWSER=curl incus remote add --accept-certificate oidc "${INCUS_ADDR}" --auth-type oidc
    [ "$(incus info oidc: | grep ^auth_user_name | sed "s/.*: //g")" = "test-user" ]
    incus remote remove oidc

    # Opaque access tokens are rejected when an audience is configured
    incus config set "oidc.audience=incus"
    ! BROWSER=curl incus remote add --accept-certificate oidc "${INCUS_ADDR}" --auth-type oidc || false
    incus remote remove oidc 2>/dev/null || true
    incus config unset "oidc.audience"

    # Cleanup OIDC
    kill_oidc
    incus config unset oidc.issuer
    incus config unset oidc.client.id
}
