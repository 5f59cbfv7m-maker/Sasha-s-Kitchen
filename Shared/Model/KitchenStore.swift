import Foundation
import SwiftData

/// База приложения и часов: одна схема, один контейнер iCloud.
enum KitchenStore {
    /// Одна база на процесс: её берут и экраны, и Siri с Командами, которые
    /// запускают приложение в фоне. `-resetData YES` — отдельная база без iCloud.
    static let shared = makeContainer(isTestRun: UserDefaults.standard.bool(forKey: "resetData"))

    static let schema = Schema([Product.self, StockItem.self, Recipe.self,
                                RecipeIngredient.self, CookingLog.self, ShoppingItem.self])

    /// Общая папка приложения, виджетов и часов. На Mac виджетов нет, и база
    /// остаётся где была.
    static let appGroup = "group.com.kirillrychkov.FridgeOracle"

    /// Виджет только читает базу и отмечает купленное; синхронизирует её с
    /// iCloud приложение, иначе два процесса гоняли бы одно и то же.
    static var isExtension: Bool { Bundle.main.bundleURL.pathExtension == "appex" }

    /// Обычная база синхронизируется через iCloud (приватная база CloudKit
    /// пользователя). Тестовая лежит в отдельном файле и без iCloud: сброс в
    /// UI-тесте иначе стёр бы данные на всех устройствах.
    static func makeContainer(isTestRun: Bool = false) -> ModelContainer {
        let configuration: ModelConfiguration
        if isTestRun {
            configuration = ModelConfiguration("UITests", schema: schema, cloudKitDatabase: .none)
        } else if let group = groupDirectory {
            moveStore(into: group)
            configuration = ModelConfiguration(schema: schema, groupContainer: .identifier(appGroup),
                                               cloudKitDatabase: isExtension ? .none : .automatic)
        } else {
            configuration = ModelConfiguration(schema: schema, groupContainer: .none, cloudKitDatabase: .automatic)
        }
        do {
            return try ModelContainer(for: schema, configurations: configuration)
        } catch {
            fatalError("Не удалось открыть базу SwiftData: \(error)")
        }
    }

    /// Папка базы в App Group; nil — на Mac и без entitlement (сборка без подписи).
    private static var groupDirectory: URL? {
        #if targetEnvironment(macCatalyst)
        return nil
        #else
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)?
            .appending(path: "Library/Application Support")
        #endif
    }

    /// Версия 1.0 хранила базу в своей папке приложения, виджеты её не видят.
    /// Переносим один раз, до открытия: иначе SwiftData завёл бы пустую базу,
    /// и данные пришлось бы ждать из iCloud заново.
    private static func moveStore(into group: URL) {
        guard !isExtension else { return }
        let files = FileManager.default
        let old = URL.applicationSupportDirectory
        guard !files.fileExists(atPath: group.appending(path: "default.store").path),
              files.fileExists(atPath: old.appending(path: "default.store").path) else { return }
        try? files.createDirectory(at: group, withIntermediateDirectories: true)
        for name in ["default.store", "default.store-shm", "default.store-wal"] {
            try? files.copyItem(at: old.appending(path: name), to: group.appending(path: name))
        }
    }
}
