#!/bin/sh -eu

echo "Checking that functional blocks are followed by newlines..."

# Check all .go files except the protobuf bindings (.pb.go)
files=$(git ls-files --cached --modified --others '*.go' ':!:*.pb.go' ':!:test/mini-oidc/storage/*.go' ':!:internal/server/network/*/schema/*/*.go')

exit_code=0
for file in $files
do
  # This oneliner has a few steps:
  # 1. sed:
  #     a. Check for lines that contain a single closing brace (plus whitespace).
  #     b. Move the pattern space window forward to the next line.
  #     c. Match lines that start with a word character. This allows for a closing brace on subsequent lines.
  #     d. Print the line number.
  # 2. xargs: Print the filename next to the line number of the matches (piped).
  # 3. If there were no matches, the file name without the line number is printed, use grep to filter it out.
  # 4. Replace the space with a colon to make a clickable link.
  RESULT=$(sed -n -e '/^\s*}\s*$/{n;/^\s*\w/{;=}}' "$file" | xargs -L 1 echo "$file" | grep -v '\.go$' | sed 's/ /:/g')
  if [ -n "${RESULT}" ]; then
    echo "${RESULT}"
    exit_code=1
  fi
done

exit $exit_code
