#!/bin/zsh
# Ставит Debug-сборку на подключённое устройство, подписав личной командой
# (пока в Xcode нет команды J656PYWTCP). Личная команда не умеет iCloud:
# синхронизация, общий список и семейная кухня в такой сборке выключены
# (SK_CLOUDKIT=NO), остальное работает. Профиль живёт 7 дней.
#
#   Scripts/install-device.sh                 # первый подключённый iPad
#   Scripts/install-device.sh <UDID>
set -euo pipefail
cd "${0:A:h}/.."
export DEVELOPER_DIR=${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}
TEAM=${TEAM:-QU2NF6T447}
DEVICE=${1:-$(xcrun devicectl list devices 2>/dev/null | grep physical | grep iPad \
  | grep -oE '[0-9A-F]{8}-[0-9A-F]{16}' | head -1 || true)}
[[ -n "$DEVICE" ]] || { echo "Нет подключённого устройства"; exit 1; }
DERIVED=${TMPDIR:-/tmp}/kitchen-device
xcodebuild -project FridgeOracle.xcodeproj -scheme FridgeOracle -configuration Debug \
  -destination "id=$DEVICE" -derivedDataPath "$DERIVED" -allowProvisioningUpdates \
  DEVELOPMENT_TEAM=$TEAM CODE_SIGN_ENTITLEMENTS=Config/PersonalTeam.entitlements SK_CLOUDKIT=NO \
  build | grep -E "error:|BUILD" || true
xcrun devicectl device install app --device "$DEVICE" "$DERIVED/Build/Products/Debug-iphoneos/FridgeOracle.app"
xcrun devicectl device process launch --device "$DEVICE" com.kirillrychkov.FridgeOracle
