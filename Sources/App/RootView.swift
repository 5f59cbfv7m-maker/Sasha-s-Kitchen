import SwiftData
import SwiftUI
import WidgetKit

struct RootView: View {
    /// Открыта чужая семейная кухня — с каким доступом; nil — своя.
    var familyAccess: KitchenAccess?

    @Environment(\.modelContext) private var context
    @Query private var stock: [StockItem]
    @Query(filter: #Predicate<ShoppingItem> { !$0.isBought }) private var listed: [ShoppingItem]
    @Query private var products: [Product]
    @Query private var recipes: [Recipe]

    /// Вкладка, с которой открывается приложение. Обычно «Холодильник», но
    /// её можно задать аргументом запуска — так снимаются скриншоты экранов
    /// без ручных нажатий:
    ///   xcrun simctl launch <device> com.kirillrychkov.FridgeOracle -startTab recipes
    @State private var selection: TabID = .initial
    /// Заставка при холодном старте; при возврате из фона не показывается.
    @State private var showIntro = !IntroView.isSkipped

    enum TabID: String, CaseIterable {
        case fridge, shopping, recipes, plan, store, log, settings

        static var initial: TabID {
            TabID(rawValue: UserDefaults.standard.string(forKey: "startTab") ?? "") ?? .fridge
        }

        var title: String {
            switch self {
            case .fridge: String(localized: "Холодильник")
            case .shopping: String(localized: "Для покупки")
            case .recipes: String(localized: "Рецепты")
            case .plan: String(localized: "Меню")
            case .store: String(localized: "Магазин")
            case .log: String(localized: "Журнал")
            case .settings: String(localized: "Настройки")
            }
        }

        var symbol: String {
            switch self {
            case .fridge: "refrigerator"
            case .shopping: "cart"
            case .recipes: "fork.knife"
            case .plan: "calendar"
            case .store: "storefront"
            case .log: "clock.arrow.circlepath"
            case .settings: "gearshape"
            }
        }
    }

    /// Магазин виден, только когда задан адрес сервера (`StoreAPI`).
    static var showsStore: Bool { StoreAPI.isConfigured }

    /// Меняется, когда у остатка меняется срок или он кончается, — тогда
    /// уведомления о сроках планируются заново.
    private var expirySignature: [String] {
        stock.map { "\($0.name)|\($0.expiryDate?.timeIntervalSinceReferenceDate ?? 0)|\($0.isEmpty)" }
    }

    private var shoppingCount: Int {
        Kitchen.shoppingEntries(from: stock).count + listed.count + SharedLists.shared.guestCount
    }

    var body: some View {
        // `.sidebarAdaptable` даёт боковую панель на широком iPad и плавающую
        // панель вкладок в Slide Over — то же приложение без отдельной вёрстки.
        TabView(selection: $selection) {
            Tab(TabID.fridge.title, systemImage: TabID.fridge.symbol, value: .fridge) {
                FridgeView()
            }
            // В семейной кухне с доступом «только холодильник» — только он.
            if familyAccess != .fridge {
                Tab(TabID.shopping.title, systemImage: TabID.shopping.symbol, value: .shopping) {
                    ShoppingListView()
                }
                .badge(shoppingCount)
                Tab(TabID.recipes.title, systemImage: TabID.recipes.symbol, value: .recipes) {
                    RecipesView()
                }
                Tab(TabID.plan.title, systemImage: TabID.plan.symbol, value: .plan) {
                    MealPlanView()
                }
                // Без адреса сервера вкладки нет совсем: пустой или «скоро»
                // магазин ревью отклоняет (Guideline 2.1). См. Sources/Store/ и server/.
                if Self.showsStore {
                    Tab(TabID.store.title, systemImage: TabID.store.symbol, value: .store) {
                        StoreView()
                    }
                }
                Tab(TabID.log.title, systemImage: TabID.log.symbol, value: .log) {
                    CookingLogView()
                }
            }
            Tab(TabID.settings.title, systemImage: TabID.settings.symbol, value: .settings) {
                SettingsView()
            }
        }
        .tabViewStyle(.sidebarAdaptable)
        // Число записей меняется и когда iCloud привозит чужие: самое время
        // слить одноимённые дубликаты.
        .task(id: products.count + recipes.count) { SyncCleanup.run(in: context) }
        .modifier(SharedListSync())
        .task(id: expirySignature) { await ExpiryReminders.reschedule(in: context) }
        // Виджеты и «Поход в магазин» показывают то же, что вкладка «Для покупки».
        .task(id: expirySignature + listed.map { "\($0.name)|\($0.isBought)" }) {
            WidgetCenter.shared.reloadAllTimelines()
            #if os(iOS) && !targetEnvironment(macCatalyst)
            await ShoppingTrip.update(in: context)
            #endif
        }
        .overlay {
            if showIntro {
                IntroView { withAnimation(.easeOut(duration: 0.45)) { showIntro = false } }
                    .transition(.opacity)
            }
        }
    }
}
