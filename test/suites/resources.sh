test_resources() {
    RES=$(incus storage show --resources "incustest-$(basename "${INCUS_DIR}")")
    echo "${RES}" | grep -q "^space:"

    RES=$(incus info --resources)
    echo "${RES}" | grep -q "^CPU"
    echo "${RES}" | grep -q "Cores:"
    echo "${RES}" | grep -q "Threads:"
    echo "${RES}" | grep -q "Free:"
    echo "${RES}" | grep -q "Used:"
    echo "${RES}" | grep -q "Total:"
}
