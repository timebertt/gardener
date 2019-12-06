#!/bin/bash

CURRENT_DIR=$(dirname $0)

if ! which dot > /dev/null ; then
  >&2 echo "dot not installed"
  exit 1
fi

echo "=> Generating dot graph"
go run $CURRENT_DIR/graph-gen.go --output $CURRENT_DIR/graph-gen.gv
if [ $? -ne 0 ] ; then
  >&2 echo "Failed to generate dot graph"
  exit 1
fi
echo "Successfully generated dot graph"

echo "=> Generating svg file"
dot -Tsvg -o $CURRENT_DIR/graph-gen.svg $CURRENT_DIR/graph-gen.gv
if [ $? -ne 0 ] ; then
  >&2 echo "Failed to generate svg file"
  exit 1
fi
echo "Successfully generated svg file"
