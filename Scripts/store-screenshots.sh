#!/bin/zsh
# Скриншоты для App Store: Debug-сборка на симуляторах, которые требует App
# Store Connect (iPhone 6.9", iPad 13", Apple Watch), и Mac-версия на этом Mac.
# Кухня — стартовый набор плюс демо (`-resetData YES -storeDemo YES`) в
# отдельной базе без iCloud: настоящие данные не трогаются.
#
#   Scripts/store-screenshots.sh ja                    # → docs/store-raw/ja/{iphone,ipad,mac,watch}
#   DEVICES="iphone mac" Scripts/store-screenshots.sh en
#
# Размеры — ровно те, что принимает App Store Connect: 1320×2868, 2064×2752,
# 1440×900 (2880×1800 на Retina) и 422×514. Подписи и фон добавляет
# Scripts/frame-screenshots.swift.
set -euo pipefail
cd "$(dirname "$0")/.."

LANGUAGE=${1:-en}
OUT="${STORE_OUT:-docs/store-raw}/$LANGUAGE"
DD="${TMPDIR:-/tmp}/kitchen-screenshots"
BUNDLE=com.kirillrychkov.FridgeOracle
typeset -A LOCALES=(en en_US ru ru_RU de de_DE fr fr_FR es es_ES it it_IT pt-BR pt_BR ja ja_JP ko ko_KR zh-Hans zh_CN th th_TH)
LOCALE=${LOCALES[$LANGUAGE]:-en_US}
DEVICES=(${(z)${DEVICES:-iphone ipad mac watch}})
typeset -A SIMULATORS=(iphone "Kitchen Store iPhone" ipad "Kitchen Store iPad" watch "Kitchen Store Watch")
# Порядок загрузки: первые три видны прямо в поиске.
SHOTS=(
  "fridge|-startTab fridge"
  "shopping|-startTab shopping"
  "recipe|-startTab recipes -openRecipe Омлет_классический"
  "recipes|-startTab recipes"
  "log|-startTab log"
)
WATCH_SHOTS=(
  "list|"
  "shared|-sharedFirst YES"
)
# Демо-кухня собрана из российского набора — на всех языках одна и та же.
COMMON=(-resetData YES -storeDemo YES -starterSet russia -skipIntro YES -AppleLanguages "($LANGUAGE)" -AppleLocale $LOCALE)

# Пробел в имени рецепта не переживает разбор аргументов — внутри SHOTS он «_».
shot_args() { reply=(${(z)${1#*|}}); reply=("${(@)reply//_/ }"); }

# Ждём, пока два снимка подряд не совпадут: SwiftData раскладывает данные и
# анимирует список не мгновенно.
settle() {
  local previous="" current same=0
  sleep ${WAIT_FOR:-6}
  for _ in {1..15}; do
    "$@" "$DD/probe.png" >/dev/null 2>&1
    sips -Z 64 "$DD/probe.png" --out "$DD/probe-s.png" >/dev/null
    current=$(md5 -q "$DD/probe-s.png")
    [[ "$current" == "$previous" ]] && same=$((same + 1)) || same=0
    (( same >= 1 )) && return
    previous=$current
    sleep 2
  done
}

udid() { xcrun simctl list devices available | grep -F "$1 (" | head -1 | sed -E 's/.*\(([0-9A-F-]{36})\).*/\1/'; }

boot() {
  local id=$1
  xcrun simctl boot "$id" 2>/dev/null || true
  xcrun simctl bootstatus "$id" -b >/dev/null
  # Дата в строке состояния и формат чисел — по языку симулятора.
  if [[ "$(xcrun simctl spawn "$id" defaults read -g AppleLocale 2>/dev/null)" != "$LOCALE" ]]; then
    xcrun simctl spawn "$id" defaults write -g AppleLanguages -array "$LANGUAGE"
    xcrun simctl spawn "$id" defaults write -g AppleLocale "$LOCALE"
    xcrun simctl shutdown "$id"
    xcrun simctl boot "$id"
    xcrun simctl bootstatus "$id" -b >/dev/null
  fi
}

mkdir -p "$DD"
APP="$DD/Build/Products/Debug-iphonesimulator/FridgeOracle.app"
if [[ ! -d "$APP" || -n ${REBUILD:-} ]]; then
  xcodebuild -quiet -project FridgeOracle.xcodeproj -scheme FridgeOracle -configuration Debug \
    -destination 'generic/platform=iOS Simulator' -derivedDataPath "$DD" CODE_SIGNING_ALLOWED=NO build
fi

for DEVICE in $DEVICES; do
  DIR="$OUT/$DEVICE"
  mkdir -p "$DIR"
  case $DEVICE in
  iphone|ipad)
    ID=$(udid "${SIMULATORS[$DEVICE]}")
    [[ -n "$ID" ]] || { echo "Нет симулятора ${SIMULATORS[$DEVICE]}"; exit 1; }
    boot "$ID"
    xcrun simctl install "$ID" "$APP"
    xcrun simctl status_bar "$ID" override --time 9:41 --batteryState charged --batteryLevel 100 \
      --wifiBars 3 --cellularBars 4 >/dev/null
    [[ $DEVICE == ipad ]] && WAIT_FOR=12 || WAIT_FOR=7
    N=0
    for SHOT in $SHOTS; do
      N=$((N + 1))
      xcrun simctl terminate "$ID" $BUNDLE 2>/dev/null || true
      shot_args $SHOT
      xcrun simctl launch "$ID" $BUNDLE "${reply[@]}" "${COMMON[@]}" >/dev/null
      settle xcrun simctl io "$ID" screenshot
      xcrun simctl io "$ID" screenshot "$DIR/$N-${SHOT%%|*}.png" >/dev/null 2>&1
      echo "$DIR/$N-${SHOT%%|*}.png"
    done
    xcrun simctl terminate "$ID" $BUNDLE 2>/dev/null || true
    ;;
  watch)
    ID=$(udid "${SIMULATORS[watch]}")
    [[ -n "$ID" ]] || { echo "Нет симулятора ${SIMULATORS[watch]}"; exit 1; }
    boot "$ID"
    xcrun simctl install "$ID" "$APP/Watch/FridgeOracleWatch.app"
    xcrun simctl status_bar "$ID" override --time 9:41 >/dev/null 2>&1 || true
    WAIT_FOR=8
    N=0
    for SHOT in $WATCH_SHOTS; do
      N=$((N + 1))
      xcrun simctl terminate "$ID" $BUNDLE.watchkitapp 2>/dev/null || true
      shot_args $SHOT
      xcrun simctl launch "$ID" $BUNDLE.watchkitapp "${reply[@]}" "${COMMON[@]}" >/dev/null
      settle xcrun simctl io "$ID" screenshot
      xcrun simctl io "$ID" screenshot "$DIR/$N-${SHOT%%|*}.png" >/dev/null 2>&1
      echo "$DIR/$N-${SHOT%%|*}.png"
    done
    xcrun simctl terminate "$ID" $BUNDLE.watchkitapp 2>/dev/null || true
    ;;
  mac)
    # Mac-версия без подписи: без entitlements нет iCloud и песочницы, а
    # `-resetData` и так кладёт базу «UITests» отдельно от настоящей.
    MAC_APP="$DD/Build/Products/Debug-maccatalyst/FridgeOracle.app"
    if [[ ! -d "$MAC_APP" || -n ${REBUILD:-} ]]; then
      xcodebuild -quiet -project FridgeOracle.xcodeproj -scheme FridgeOracle -configuration Debug \
        -destination 'platform=macOS,variant=Mac Catalyst' -derivedDataPath "$DD" CODE_SIGNING_ALLOWED=NO build
    fi
    WAIT_FOR=6
    N=0
    for SHOT in $SHOTS; do
      N=$((N + 1))
      pkill -f "kitchen-screenshots/Build/" 2>/dev/null && sleep 1
      shot_args $SHOT
      open -n "$MAC_APP" --args -ApplePersistenceIgnoreState YES "${reply[@]}" "${COMMON[@]}" -windowSize 1440x900
      sleep 2
      PID=$(pgrep -n -f "kitchen-screenshots/Build/")
      WID=""
      for _ in {1..30}; do
        WID=$(swift -e 'import CoreGraphics
          let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]] ?? []
          if let w = windows.first(where: { $0[kCGWindowOwnerPID as String] as? Int == Int(CommandLine.arguments[1])
              && ($0[kCGWindowBounds as String] as? [String: Double])?["Width"] == 1440 }) { print(w[kCGWindowNumber as String]!) }' "$PID")
        [[ -n "$WID" ]] && break
        sleep 1
      done
      [[ -n "$WID" ]] || { echo "Окно Mac так и не стало 1440×900"; exit 1; }
      # Запущенное в фоне окно снимается неактивным (серые выделения):
      # повторное открытие выводит приложение вперёд.
      open "$MAC_APP"
      settle screencapture -x -o -l "$WID"
      screencapture -x -o -l "$WID" "$DD/window.png"
      # Прозрачность App Store Connect не принимает: углы окна — цветом заголовка.
      swift -e 'import AppKit
        let a = CommandLine.arguments, rep = NSBitmapImageRep(data: try! Data(contentsOf: URL(fileURLWithPath: a[1])))!
        let out = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: rep.pixelsWide, pixelsHigh: rep.pixelsHigh,
          bitsPerSample: 8, samplesPerPixel: 3, hasAlpha: false, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 32)!
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: out)
        rep.colorAt(x: rep.pixelsWide / 2, y: 4)!.setFill()
        NSRect(x: 0, y: 0, width: rep.pixelsWide, height: rep.pixelsHigh).fill()
        rep.draw(in: NSRect(x: 0, y: 0, width: rep.pixelsWide, height: rep.pixelsHigh))
        NSGraphicsContext.current?.flushGraphics()
        try! out.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: a[2]))' "$DD/window.png" "$DIR/$N-${SHOT%%|*}.png"
      echo "$DIR/$N-${SHOT%%|*}.png"
    done
    pkill -f "kitchen-screenshots/Build/" 2>/dev/null || true
    ;;
  esac
done
