import SwiftData
import SwiftUI

@main
struct FridgeOracleApp: App {
    let container: ModelContainer

    init() {
        // `-resetData YES` в аргументах запуска стирает базу и раскладывает
        // стартовые данные заново — UI-тестам нужен одинаковый холодильник
        // на каждом прогоне, а база переживает переустановку приложения.
        let isTestRun = UserDefaults.standard.bool(forKey: "resetData")
        container = Self.makeContainer(isTestRun: isTestRun)
        if isTestRun { SeedLoader.reset(container.mainContext) }
        Feedback.shared.warmUp()
    }

    static let schema = Schema([Product.self, StockItem.self, Recipe.self,
                                RecipeIngredient.self, CookingLog.self, ShoppingItem.self])

    /// Обычная база синхронизируется через iCloud (приватная база CloudKit
    /// пользователя). Тестовая лежит в отдельном файле и без iCloud: сброс в
    /// UI-тесте иначе стёр бы данные на всех устройствах.
    private static func makeContainer(isTestRun: Bool) -> ModelContainer {
        let configuration = isTestRun
            ? ModelConfiguration("UITests", schema: schema, cloudKitDatabase: .none)
            : ModelConfiguration(schema: schema, cloudKitDatabase: .automatic)
        do {
            return try ModelContainer(for: schema, configurations: configuration)
        } catch {
            fatalError("Не удалось открыть базу SwiftData: \(error)")
        }
    }

    var body: some Scene {
        WindowGroup {
            RootView()
        }
        .modelContainer(container)
    }
}
