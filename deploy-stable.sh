#!/bin/bash
set -e

cd /Users/lzw/code/my/go/picoclaw

echo "Building picoclaw with version info..."
make build

VERSION=$(./build/picoclaw version 2>&1 | head -1 | awk '{print $3}')
echo "Built version: $VERSION"

echo "Copying to /usr/local/bin/picoclaw-stable..."
cp ./build/picoclaw /usr/local/bin/picoclaw-stable

echo "Restarting supervisor service..."
sudo /usr/local/bin/supervisord ctl restart picoclaw

sleep 2
echo ""
echo "Deployed version:"
/usr/local/bin/picoclaw-stable version

echo ""
/usr/local/bin/supervisord ctl status picoclaw
