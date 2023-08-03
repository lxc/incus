test_console() {
  lxc_version=$(inc info | awk '/driver_version:/ {print $NF}')
  lxc_major=$(echo "${lxc_version}" | cut -d. -f1)

  if [ "${lxc_major}" -lt 3 ]; then
    echo "==> SKIP: The console ringbuffer require liblxc 3.0 or higher"
    return
  fi

  echo "==> API extension console"

  ensure_import_testimage

  inc init testimage cons1

  inc start cons1

  # Make sure there's something in the console ringbuffer.
  echo 'some content' | inc exec cons1 -- tee /dev/console
  echo 'some more content' | inc exec cons1 -- tee /dev/console

  # Retrieve the ringbuffer contents.
  inc console cons1 --show-log | grep 'some content'

  inc stop --force cons1

  # Retrieve on-disk representation of the console ringbuffer.
  inc console cons1 --show-log | grep 'some more content'

  inc delete --force cons1
}
