import SwiftData

/// База приложения и часов: одна схема, один контейнер iCloud.
enum KitchenStore {
    static let schema = Schema([Product.self, StockItem.self, Recipe.self,
                                RecipeIngredient.self, CookingLog.self, ShoppingItem.self])

    /// Обычная база синхронизируется через iCloud (приватная база CloudKit
    /// пользователя). Тестовая лежит в отдельном файле и без iCloud: сброс в
    /// UI-тесте иначе стёр бы данные на всех устройствах.
    static func makeContainer(isTestRun: Bool = false) -> ModelContainer {
        let configuration = isTestRun
            ? ModelConfiguration("UITests", schema: schema, cloudKitDatabase: .none)
            : ModelConfiguration(schema: schema, cloudKitDatabase: .automatic)
        do {
            return try ModelContainer(for: schema, configurations: configuration)
        } catch {
            fatalError("Не удалось открыть базу SwiftData: \(error)")
        }
    }
}
