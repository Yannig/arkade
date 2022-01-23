#!/bin/bash
ARKADE_CMD=${ARKADE_CMD:-"./bin/arkade"}
ARKADE_BIN_PATH=${ARKADE_BIN_PATH:-"$HOME/.arkade/bin"}
START_TEST=${START_TEST:-""}

for tool in $($ARKADE_CMD get -o list | sort -u)
do
  if [ "$tool" = "$START_TEST" ]; then START_TEST="" ; fi
  if [ ! -z "$START_TEST" ]; then echo "Skipping $tool"; continue ; fi
  rm -f $ARKADE_BIN_PATH/$tool
  $ARKADE_CMD get $tool
  if [ ! -f $ARKADE_BIN_PATH/$tool ]; then
    echo "Error downloading $tool"
    exit 1
  fi
  file_kind=$(file $ARKADE_BIN_PATH/$tool)
  if echo "$file_kind" | grep ELF; then continue ; fi
  if echo "$file_kind" | grep script; then continue ; fi
  echo "ERROR: Something is wrong with $tool"
  read
done
