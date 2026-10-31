import AppIntents
import Foundation
import SwiftData

/// Siri, Команды и Spotlight: вписать покупку, спросить, что купить и что
/// скоро испортится, отметить купленное. Работают без открытия приложения —
/// система запускает его в фоне, данные — та же база (`KitchenStore.shared`).
/// На часах те же действия выполняет приложение для часов.

struct AddToShoppingListIntent: AppIntent {
    static let title: LocalizedStringResource = "Добавить в список покупок"
    static let description = IntentDescription("Вписывает продукт в список покупок Sasha’s Kitchen.")

    @Parameter(title: "Что купить", requestValueDialog: "Что добавить в список?")
    var item: String

    @MainActor
    func perform() async throws -> some IntentResult & ProvidesDialog {
        let context = KitchenStore.shared.mainContext
        guard let added = Kitchen.addToList(item, in: context) else {
            return .result(dialog: "Не понял, что добавить.")
        }
        return .result(dialog: "Добавил «\(added.name)» в список покупок.")
    }
}

struct ShoppingListIntent: AppIntent {
    static let title: LocalizedStringResource = "Что купить"
    static let description = IntentDescription("Называет всё, что сейчас в списке покупок.")

    @MainActor
    func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<[String]> {
        let names = Self.names(in: KitchenStore.shared.mainContext)
        guard !names.isEmpty else {
            return .result(value: [], dialog: "Список покупок пуст — в холодильнике всё есть.")
        }
        return .result(value: names, dialog: "Купить: \(names.formatted(.list(type: .and)))")
    }

    /// Своё вписанное и то, что кончается в холодильнике, — как на вкладке.
    @MainActor
    static func names(in context: ModelContext) -> [String] {
        let listed = ((try? context.fetch(FetchDescriptor<ShoppingItem>(sortBy: [SortDescriptor(\.createdAt)]))) ?? [])
            .filter { !$0.isBought }.map(\.name)
        let fridge = Kitchen.shoppingEntries(in: context).map(\.item.name)
        return listed + fridge.filter { name in !listed.contains { $0.sameName(as: name) } }
    }
}

struct ExpiringSoonIntent: AppIntent {
    static let title: LocalizedStringResource = "Что скоро испортится"
    static let description = IntentDescription("Называет продукты, у которых в ближайшие два дня кончается срок.")

    @MainActor
    func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<[String]> {
        let names = Kitchen.allStock(in: KitchenStore.shared.mainContext)
            .filter { !$0.isEmpty && $0.isExpiringSoon }
            .sorted { ($0.expiryDate ?? .distantFuture) < ($1.expiryDate ?? .distantFuture) }
            .map(\.name)
        guard !names.isEmpty else {
            return .result(value: [], dialog: "В ближайшие дни ничего не испортится.")
        }
        return .result(value: names, dialog: "Скоро испортится: \(names.formatted(.list(type: .and)))")
    }
}

/// «Купил» по имени — из виджета и Команд. Своё вписанное отмечается, а то,
/// что кончается в холодильнике, пополняется предложенным количеством.
struct MarkBoughtIntent: AppIntent {
    static let title: LocalizedStringResource = "Отметить купленным"
    static let isDiscoverable = false

    @Parameter(title: "Продукт")
    var name: String

    init() {}

    init(name: String) {
        self.name = name
    }

    @MainActor
    func perform() async throws -> some IntentResult {
        Self.markBought(name, in: KitchenStore.shared.mainContext)
        return .result()
    }

    @MainActor
    static func markBought(_ name: String, in context: ModelContext) {
        let listed = ((try? context.fetch(FetchDescriptor<ShoppingItem>())) ?? [])
            .first { !$0.isBought && $0.name.sameName(as: name) }
        if let listed {
            Kitchen.setBought(listed, true, in: context)
        } else if let entry = Kitchen.shoppingEntries(in: context).first(where: { $0.item.name.sameName(as: name) }) {
            Kitchen.replenish(entry.item, by: entry.suggested)
            try? context.save()
        }
    }
}

// Фразы Siri объявляет только приложение; в виджете — те же действия без них.
#if !WIDGET_EXTENSION
struct KitchenShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(intent: AddToShoppingListIntent(), phrases: [
            "Добавь в список покупок в \(.applicationName)",
            "Добавить покупку в \(.applicationName)",
        ], shortTitle: "Добавить в список", systemImageName: "cart.badge.plus")
        AppShortcut(intent: ShoppingListIntent(), phrases: [
            "Что купить в \(.applicationName)",
            "Покажи список покупок в \(.applicationName)",
        ], shortTitle: "Что купить", systemImageName: "cart")
        AppShortcut(intent: ExpiringSoonIntent(), phrases: [
            "Что скоро испортится в \(.applicationName)",
            "Что истекает в \(.applicationName)",
        ], shortTitle: "Что испортится", systemImageName: "clock.badge.exclamationmark")
    }
}
#endif
