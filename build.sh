#!/bin/bash
# Build FAIF-Scanner: Angular client + Go server
# The webapp is embedded into the Go binary via //go:embed
# so BOTH must be rebuilt for changes to take effect.
#
# Usage:
#   ./build.sh           Build only (no restart)
#   ./build.sh test      Build + restart test instance (port 3001)
#   ./build.sh prod      Build + restart production (port 3000)
#   ./build.sh both      Build + restart both
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

case "${1:-}" in
    test)
        echo "=== Restarting test instance ==="
        sudo systemctl restart faif-scanner-test
        sleep 2
        sudo systemctl status faif-scanner-test --no-pager | head -5
        ;;
    prod)
        echo "=== Restarting production ==="
        sudo systemctl restart faif-scanner-go
        sleep 2
        sudo systemctl status faif-scanner-go --no-pager | head -5
        ;;
    both)
        echo "=== Restarting both instances ==="
        sudo systemctl restart faif-scanner-test
        sudo systemctl restart faif-scanner-go
        sleep 2
        sudo systemctl status faif-scanner-test --no-pager | head -3
        sudo systemctl status faif-scanner-go --no-pager | head -3
        ;;
    "")
        echo "(no restart — pass 'test', 'prod', or 'both' to restart)"
        ;;
    *)
        echo "Unknown argument: $1 (use test, prod, or both)"
        exit 1
        ;;
esac
