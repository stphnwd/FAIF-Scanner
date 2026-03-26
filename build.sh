#!/bin/bash
# Build FAIF-Scanner: Angular client + Go server
# The webapp is embedded into the Go binary via //go:embed
# so BOTH must be rebuilt for changes to take effect.
set -e

cd "$(dirname "$0")"

echo "=== Building Angular client ==="
cd client
npm run build
cd ..

echo "=== Building Go server ==="
cd server
go build -o ../rdio-scanner .
cd ..

echo "=== Build complete ==="
ls -lh rdio-scanner
