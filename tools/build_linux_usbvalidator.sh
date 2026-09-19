#!/usr/bin/env bash
set -e

echo "========================================"
echo " Building Linux USB Validator..."
echo "========================================"

cd "$(dirname "$0")/.."

GOOS=linux GOARCH=amd64 go build -o usbvalidator_linux agent/cmd/usbvalidator/main.go

echo "[OK] Built usbvalidator_linux for linux/amd64"
echo "Binary is located at: $(pwd)/usbvalidator_linux"
