import SwiftData
import SwiftUI

@main
struct FridgeOracleApp: App {
    let container: ModelContainer

    init() {
        do {
            container = try ModelContainer(
                for: Product.self, StockItem.self, Recipe.self,
                RecipeIngredient.self, CookingLog.self)
        } catch {
            fatalError("Не удалось открыть базу SwiftData: \(error)")
        }
        // `-resetData YES` в аргументах запуска стирает базу и раскладывает
        // стартовые данные заново — UI-тестам нужен одинаковый холодильник
        // на каждом прогоне, а база переживает переустановку приложения.
        if UserDefaults.standard.bool(forKey: "resetData") {
            SeedLoader.reset(container.mainContext)
        } else {
            SeedLoader.loadIfNeeded(into: container.mainContext)
        }
        Feedback.shared.warmUp()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                // Интерфейс только русский, поэтому и числа с датами везде
                // русские: иначе поля ввода показывают «4.5», а остальной
                // экран — «4,5».
                .environment(\.locale, Locale(identifier: "ru_RU"))
        }
        .modelContainer(container)
    }
}
