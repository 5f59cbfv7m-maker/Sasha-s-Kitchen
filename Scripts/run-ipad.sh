#!/bin/bash
# Собрать и запустить приложение на симуляторе iPad.
#   ./Scripts/run-ipad.sh [имя симулятора]
set -euo pipefail

DEVICE="${1:-iPad Pro 11-inch (M5)}"
export DEVELOPER_DIR="${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}"
cd "$(dirname "$0")/.."

UDID=$(xcrun simctl list devices available \
    | grep -F "$DEVICE (" | head -1 | sed -E 's/.*\(([0-9A-F-]{36})\).*/\1/')
if [ -z "$UDID" ]; then
    echo "Симулятор «$DEVICE» не найден. Доступные iPad:"
    xcrun simctl list devices available | grep -i ipad
    exit 1
fi

xcrun simctl boot "$UDID" 2>/dev/null || true
open -a Simulator

xcodebuild -project FridgeOracle.xcodeproj -scheme FridgeOracle \
    -destination "platform=iOS Simulator,id=$UDID" -configuration Debug build

APP=$(xcodebuild -project FridgeOracle.xcodeproj -scheme FridgeOracle \
    -configuration Debug -showBuildSettings 2>/dev/null \
    | awk '/ BUILT_PRODUCTS_DIR =/ {print $3}' | head -1)
APP="${APP/iphoneos/iphonesimulator}/FridgeOracle.app"

xcrun simctl install "$UDID" "$APP"
xcrun simctl launch --terminate-running-process "$UDID" com.kirillrychkov.FridgeOracle
echo "Запущено на «$DEVICE»."
