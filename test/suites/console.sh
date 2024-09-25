test_console() {
  echo "==> API extension console"

  ensure_import_testimage

  incus init testimage cons1

  incus start cons1

  # Make sure there's something in the console ringbuffer.
  echo 'some content' | incus exec cons1 -- tee /dev/console
  echo 'some more content' | incus exec cons1 -- tee /dev/console

  # Retrieve the ringbuffer contents.
  incus console cons1 --show-log | grep 'some content'

  incus stop --force cons1

  # Retrieve on-disk representation of the console ringbuffer.
  incus console cons1 --show-log | grep 'some more content'

  incus delete --force cons1
}
