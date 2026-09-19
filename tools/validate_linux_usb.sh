#!/usr/bin/env bash

# Exit immediately if a command exits with a non-zero status
set -e

echo "================================================="
echo " Phase 2E.3.2 Linux Real-Host Validation Script"
echo "================================================="

# 1. Verify OS
if [ "$(uname -s)" != "Linux" ]; then
    echo "[ERROR] This script must be run on a Linux host."
    exit 1
fi

# 2. Print System Info
echo "--- System Information ---"
uname -a
if [ -f /etc/os-release ]; then
    cat /etc/os-release | grep -E "^(NAME|VERSION|PRETTY_NAME)="
fi
echo "--------------------------"

# 3. Verify Validator Exists
VALIDATOR_BIN="$(dirname "$0")/../usbvalidator_linux"
if [ ! -f "$VALIDATOR_BIN" ]; then
    echo "[ERROR] Validator binary not found at $VALIDATOR_BIN"
    echo "Please build it first using build_linux_usbvalidator.sh or ensure it's transferred to the Linux VM."
    exit 1
fi

# 4. Verify Execute Permission (and add if missing)
if [ ! -x "$VALIDATOR_BIN" ]; then
    echo "[INFO] Adding execute permission to $VALIDATOR_BIN"
    chmod +x "$VALIDATOR_BIN"
fi

# 5. Check for netlink requirements / privileges
if [ "$EUID" -ne 0 ]; then
    echo "[WARNING] You are not running as root. Netlink sockets (NETLINK_KOBJECT_UEVENT) typically require root privileges."
    echo "[WARNING] If the validator fails to start, please run this script with sudo."
fi

# Determine log directory
LOG_DIR="$(dirname "$0")/../logs"
mkdir -p "$LOG_DIR"
TSTAMP="$(date +%Y%m%d_%H%M%S)"
VALIDATOR_LOG="$LOG_DIR/usbvalidator_$TSTAMP.log"
UDEV_LOG="$LOG_DIR/udevadm_$TSTAMP.log"

# Clean up child processes on exit
cleanup() {
    echo ""
    echo "================================================="
    echo " Stopping Validation..."
    echo "================================================="
    if [ -n "$UDEV_PID" ]; then
        kill $UDEV_PID 2>/dev/null || true
    fi
    if [ -n "$VALIDATOR_PID" ]; then
        kill $VALIDATOR_PID 2>/dev/null || true
    fi
    echo "Logs saved to:"
    echo " - $VALIDATOR_LOG"
    echo " - $UDEV_LOG"
    exit 0
}

trap cleanup SIGINT SIGTERM EXIT

# 6 & 7. Start independent reference monitor & project validator
echo "Starting independent reference monitor (udevadm)..."
if command -v udevadm >/dev/null 2>&1; then
    udevadm monitor --kernel --property --subsystem-match=usb > "$UDEV_LOG" 2>&1 &
    UDEV_PID=$!
    echo "[OK] udevadm monitor started (PID: $UDEV_PID)"
else
    echo "[WARNING] udevadm not found on this system. Cannot capture independent reference logs."
    echo "Reference monitor not running." > "$UDEV_LOG"
fi

echo "Starting RedCyberFox USB Validator..."
"$VALIDATOR_BIN" --verbose | tee "$VALIDATOR_LOG" &
VALIDATOR_PID=$!
echo "[OK] usbvalidator_linux started (PID: $VALIDATOR_PID)"

# 8 & 9. Ready for physical test
echo "================================================="
echo " VALIDATOR IS READY FOR USB LIFECYCLE TESTING"
echo "================================================="
echo "Please perform the following physical actions:"
echo " 1. Attach/Insert a physical USB device into the Linux host/VM."
echo " 2. Wait for the USB_INSERT event to appear in the logs above."
echo " 3. Detach/Remove the physical USB device."
echo " 4. Wait for the USB_REMOVE event to appear in the logs above."
echo " 5. Press Ctrl+C when done to terminate and save logs."
echo "================================================="
echo "Waiting for events..."

# Wait for children to finish
wait
