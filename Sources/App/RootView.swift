import SwiftData
import SwiftUI

struct RootView: View {
    @Query private var stock: [StockItem]

    /// Вкладка, с которой открывается приложение. Обычно «Холодильник», но
    /// её можно задать аргументом запуска — так снимаются скриншоты экранов
    /// без ручных нажатий:
    ///   xcrun simctl launch <device> com.kirillrychkov.FridgeOracle -startTab recipes
    @State private var selection: TabID = .initial
    /// Заставка при холодном старте; при возврате из фона не показывается.
    @State private var showIntro = !IntroView.isSkipped

    enum TabID: String, CaseIterable {
        case fridge, shopping, recipes, log, settings

        static var initial: TabID {
            TabID(rawValue: UserDefaults.standard.string(forKey: "startTab") ?? "") ?? .fridge
        }

        var title: String {
            switch self {
            case .fridge: "Холодильник"
            case .shopping: "Для покупки"
            case .recipes: "Рецепты"
            case .log: "Журнал"
            case .settings: "Настройки"
            }
        }

        var symbol: String {
            switch self {
            case .fridge: "refrigerator"
            case .shopping: "cart"
            case .recipes: "fork.knife"
            case .log: "clock.arrow.circlepath"
            case .settings: "gearshape"
            }
        }
    }

    private var shoppingCount: Int {
        stock.filter { $0.product != nil && ($0.isEmpty || $0.isLow) }.count
    }

    var body: some View {
        // `.sidebarAdaptable` даёт боковую панель на широком iPad и плавающую
        // панель вкладок в Slide Over — то же приложение без отдельной вёрстки.
        TabView(selection: $selection) {
            Tab(TabID.fridge.title, systemImage: TabID.fridge.symbol, value: .fridge) {
                FridgeView()
            }
            Tab(TabID.shopping.title, systemImage: TabID.shopping.symbol, value: .shopping) {
                ShoppingListView()
            }
            .badge(shoppingCount)
            Tab(TabID.recipes.title, systemImage: TabID.recipes.symbol, value: .recipes) {
                RecipesView()
            }
            Tab(TabID.log.title, systemImage: TabID.log.symbol, value: .log) {
                CookingLogView()
            }
            Tab(TabID.settings.title, systemImage: TabID.settings.symbol, value: .settings) {
                SettingsView()
            }
        }
        .tabViewStyle(.sidebarAdaptable)
        .overlay {
            if showIntro {
                IntroView { withAnimation(.easeOut(duration: 0.45)) { showIntro = false } }
                    .transition(.opacity)
            }
        }
    }
}
