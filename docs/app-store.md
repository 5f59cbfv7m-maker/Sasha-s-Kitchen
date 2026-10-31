# Выпуск в App Store

Всё, что нужно, чтобы довести Sasha’s Kitchen до магазина: чеклист, где
лежат тексты и скриншоты, что ответить в анкетах.

## 1. Чеклист

**Аккаунт**
- [ ] Apple Developer Program оплачен (команда `J656PYWTCP`, та же, что у
      Sasha’s Puzzles и Colors), соглашения в App Store Connect → Agreements приняты.
- [ ] На этом Mac в Xcode → Settings → Accounts войти в аккаунт команды
      `J656PYWTCP`: сейчас там только личная команда, она не умеет iCloud, и
      архив для App Store без этого не подпишется.

**iCloud (без этого общий список и синхронизация не заработают у людей)**
- [ ] developer.apple.com → Identifiers: у `com.kirillrychkov.FridgeOracle` и
      `com.kirillrychkov.FridgeOracle.watchkitapp` включены iCloud (CloudKit) и
      Push Notifications, контейнер `iCloud.com.kirillrychkov.FridgeOracle`.
      У них и у виджетов (`….widgets`, `….watchkitapp.widgets`) — App Groups
      `group.com.kirillrychkov.FridgeOracle`.
      У приложения ещё HealthKit (только запись КБЖУ порции в «Здоровье»).
      Xcode с автоматической подписью сделает это сам при первой сборке на
      устройство.
- [ ] Один раз запустить сборку с подписью `J656PYWTCP` на iPhone и iPad с
      разными Apple ID, пригласить в общий список, отметить покупку, дописать
      позицию; там же пригласить во всю семейную кухню (Настройки → «Семейная
      кухня») и поменять что-нибудь с обеих сторон, включая рецепт с фото —
      схема CloudKit (`Purchase`, `KitchenRecord`, `CD_*` от SwiftData)
      появится в Development.
- [ ] icloud.developer.apple.com → контейнер → **Deploy Schema Changes** в
      Production. Без этого у пользователей из App Store iCloud не работает.
- [ ] Проверить часы на настоящих Apple Watch: список приходит, «купил»
      пополняет холодильник на iPhone.

**Проект (уже сделано в репозитории)**
- [x] Bundle ID `com.kirillrychkov.FridgeOracle` (часы — `….watchkitapp`),
      автоматическая подпись, команда `J656PYWTCP`, одна запись для iPhone,
      iPad, Mac и Apple Watch.
- [x] Иконка 1024 × 1024 без альфа-канала (`AppIcon` в приложении и в `Watch/`).
- [x] Privacy Manifest `Shared/Resources/PrivacyInfo.xcprivacy`: трекинга нет,
      данные не собираются, UserDefaults — CA92.1.
- [x] Тексты разрешений (камера, Напоминания) на 11 языках (`InfoPlist.xcstrings`).
- [x] `ITSAppUsesNonExemptEncryption = NO`, `NSHumanReadableCopyright`.
- [x] Вкладка «Магазин — Скоро» скрыта (`RootView.showsStore = false`):
      заглушки недоделанных функций ревью отклоняет (Guideline 2.1).
- [x] 11 языков интерфейса: en, ru, de, fr, es, it, pt-BR, ja, ko, zh-Hans, th.
- [x] Версия `MARKETING_VERSION = 1.0`, сборка `CURRENT_PROJECT_VERSION = 1`
      у приложения и часов — они обязаны совпадать. Каждая новая загрузка —
      сборка +1 в обоих таргетах.

**Сборка**
- [ ] Xcode → схема FridgeOracle → Any iOS Device → Product → Archive →
      Distribute App → App Store Connect. Часы едут внутри этого архива.
- [ ] Mac: та же схема → My Mac (Mac Catalyst) → Archive → Distribute.

**App Store Connect → Apps → «+» → New App**
- [ ] Platforms: iOS и macOS (universal purchase). Часы отдельной платформой не
      заводятся — они часть iOS-версии.
- [ ] Name `Sasha’s Kitchen` (если занято — `Sasha’s Kitchen: Fridge & List`),
      Primary language English (U.S.), SKU `sashas-kitchen`, Bundle ID из списка.
- [ ] Category: **Food & Drink**, secondary: **Lifestyle**.
- [ ] Age Rating: анкета, везде «нет» → 4+.
- [ ] App Privacy → **Data Not Collected**. Штрихкод уходит в Open Food Facts
      (и UPCitemdb) только чтобы ответить на запрос и нигде у нас не хранится —
      по правилам Apple это не «сбор». Общий список и семейная кухня идут через
      iCloud пользователя, запись в «Здоровье» остаётся на устройстве.
- [ ] HealthKit: в описании и в интерфейсе приложение прямо говорит о записи в
      «Здоровье» (Guideline 2.5.1) — это уже есть в `store-listing.md` и в
      настройках.
- [ ] Privacy Policy URL: `https://keelspan.app/apps/sashas-kitchen/privacy/`
      (страница в репозитории `keelspan-site`, `content/apps/sashas-kitchen.json`).
      Пока домен не привязан к проекту Cloudflare Pages, она открывается на
      `https://keelspan.pages.dev/apps/sashas-kitchen/privacy/` — проверьте
      ссылку до отправки на ревью.
- [ ] Support URL: `https://keelspan.app/apps/sashas-kitchen/support/`,
      Marketing URL: `https://keelspan.app/apps/sashas-kitchen/`. Когда
      приложение выйдет, поставьте на сайте статус «В App Store» и ссылку.
- [ ] Pricing: Free и страны.
- [ ] Тексты: [`store-listing.md`](store-listing.md) — 11 языков, вставлять
      каждый в свою локализацию (App Store → «+» рядом с языком).
- [ ] Скриншоты: [`store/`](store/README.md).
- [ ] App Review Information: контакт; заметки ниже.

**Заметки для ревью (App Review Information → Notes):**

> No account is needed. On first launch the fridge is empty — tap “Load starter set” (the set matches the device region and can be changed in the menu above the button). Recipes tab: open any recipe → Cooked to deduct the ingredients; “+” → Import from a Link or Photo reads a recipe page. Menu tab: add recipes to days, then “Add What’s Missing to the List”. To Buy tab: tick items to top up the fridge; the barcode button looks a product up in Open Food Facts (camera on iPhone/iPad, typing the digits on Mac).
> Sharing: To Buy → Share → Invite to Shared List, or Settings → Family Kitchen → Invite to Kitchen (fridge only or whole kitchen), sends an iCloud invitation; the invited person (another Apple Account) sees it in their own app and can edit it. Both devices must be signed in to iCloud.
> Apple Health: Settings → Log to Health. After Cooked, the app writes calories, protein, fat and carbs of one serving; it never reads Health data.
> The Apple Watch app shows the same shopping list from iCloud. Widgets and the lock-screen shopping trip (To Buy → … → Going Shopping) show the same list.

## 2. Как пересобрать скриншоты

```bash
Scripts/store-screenshots.sh ru          # один язык, все устройства → docs/store-raw/ru/
swift Scripts/frame-screenshots.swift ru # подписи и фон → docs/store/ru/
```

Подробности — в [`store/README.md`](store/README.md).
