# Miscellaneous test checks.

check_dependencies() {
    # shellcheck disable=SC2039,3043
    local dep missing
    missing=""

    for dep in "$@"; do
        if ! command -v "$dep" > /dev/null 2>&1; then
            [ "$missing" ] && missing="$missing $dep" || missing="$dep"
        fi
    done

    if [ "$missing" ]; then
        echo "Missing dependencies: $missing" >&2
        exit 1
    fi
}

check_empty() {
    if [ "$(find "${1}" 2> /dev/null | wc -l)" -gt "1" ]; then
        echo "${1} is not empty, content:"
        find "${1}"
        false
    fi
}

check_empty_table() {
    # The profiles table will never be empty since the `default` profile cannot
    # be deleted.
    if [ "$2" = 'profiles' ]; then
        if [ -n "$(sqlite3 "${1}" "SELECT * FROM ${2} WHERE name != 'default';")" ]; then
            echo "DB table ${2} is not empty, content:"
            sqlite3 "${1}" "SELECT * FROM ${2} WHERE name != 'default';"
            return 1
        fi
        return 0
    fi

    if [ -n "$(sqlite3 "${1}" "SELECT * FROM ${2};")" ]; then
        echo "DB table ${2} is not empty, content:"
        sqlite3 "${1}" "SELECT * FROM ${2};"
        return 1
    fi
}
