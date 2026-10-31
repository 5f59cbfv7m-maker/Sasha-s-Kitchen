# Скриншоты для App Store

`<язык>/<устройство>/` — готовые к загрузке файлы, по одной папке на
локализацию App Store Connect (`pt-BR` — Portuguese (Brazil), `zh-Hans` —
Chinese (Simplified)). Загружать в том порядке, как пронумерованы: первые три
видны прямо в поиске.

| Папка | Куда в App Store Connect | Размер |
|---|---|---|
| `iphone/` | iPhone 6.9" Display | 1320 × 2868 |
| `ipad/` | iPad 13" Display | 2064 × 2752 |
| `mac/` | macOS (версия для Mac) | 1440 × 900 |
| `watch/` | Apple Watch (в iOS-версии, ниже iPad) | 422 × 514 |

Снимки iPhone и iPad App Store сам уменьшает для остальных размеров экранов.

Порядок кадров: 1 — холодильник, 2 — список покупок с общим списком,
3 — рецепт с КБЖУ, 4 — все рецепты, 5 — журнал готовки. Часы: 1 — свой
список, 2 — общий список.

## Как пересобрать

```bash
Scripts/store-screenshots.sh ja           # сырые снимки → docs/store-raw/ja/ (в git не идут)
swift Scripts/frame-screenshots.swift ja  # фон и подписи → docs/store/ja/
```

- Снимает Debug-сборку на симуляторах «Kitchen Store iPhone» (iPhone 17 Pro
  Max), «Kitchen Store iPad» (iPad Pro 13" M5) и «Kitchen Store Watch» (Ultra 3,
  49 мм) и Mac-версию на этом Mac. Если симуляторов нет — создать с этими
  именами в Xcode → Devices.
- Кухня на снимках — стартовый набор плюс демо (`-resetData YES -storeDemo YES`,
  `Shared/Seed/StoreDemo.swift`) в отдельной базе без iCloud: настоящие данные
  не трогаются. Общий список от «Маши» — без iCloud, только картинка; имя своё
  на каждом языке (ключ «Маша» в `Scripts/l10n`).
- Подписи — в `Scripts/frame-screenshots.swift`, по пять на язык. Цвета — из
  `AccentColor` приложения.
