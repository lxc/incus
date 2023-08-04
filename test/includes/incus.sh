# incus CLI related test helpers.

incus() {
    set +x
    INC_LOCAL=1 incus_remote "$@"
}

incus_remote() {
    set +x
    # shellcheck disable=SC2039,3043
    local injected cmd arg

    injected=0
    # find the path to incus binary, not the shell wrapper function
    cmd=$(unset -f incus; command -v incus)

    # shellcheck disable=SC2048,SC2068
    for arg in "$@"; do
        if [ "${arg}" = "--" ]; then
            injected=1
            cmd="${cmd} ${DEBUG:-}"
            [ -n "${INC_LOCAL}" ] && cmd="${cmd} --force-local"
            cmd="${cmd} --"
        elif [ "${arg}" = "--force-local" ]; then
            continue
        else
            cmd="${cmd} \"${arg}\""
        fi
    done

    if [ "${injected}" = "0" ]; then
        cmd="${cmd} ${DEBUG-}"
    fi
    if [ -n "${DEBUG:-}" ]; then
        eval "set -x;timeout --foreground 120 ${cmd}"
    else
        eval "timeout --foreground 120 ${cmd}"
    fi
}

gen_cert() {
    # Temporarily move the existing cert to trick incus into generating a
    # second cert.  incus will only generate a cert when adding a remote
    # server with a HTTPS scheme.  The remote server URL just needs to
    # be syntactically correct to get past initial checks; in fact, we
    # don't want it to succeed, that way we don't have to delete it later.
    [ -f "${INCUS_CONF}/${1}.crt" ] && return
    mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
    mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"
    echo y | incus_remote remote add "remote-placeholder-$$" https://0.0.0.0 || true
    mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/${1}.crt"
    mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/${1}.key"
    mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
    mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"
}
