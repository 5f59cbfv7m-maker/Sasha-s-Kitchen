import Foundation
import SwiftData

/// Кухня для скриншотов App Store (`-resetData YES -storeDemo YES`): поверх
/// стартового набора — свои позиции в списке, общий список от «Маши», пара
/// записей в журнале и меню на несколько дней. Имена — из таблицы `Seed`, так что на каждом языке свои.
enum StoreDemo {
    static var isOn: Bool { UserDefaults.standard.bool(forKey: "storeDemo") }

    static func apply(to context: ModelContext) {
        func name(_ key: String) -> String {
            Bundle.main.localizedString(forKey: key, value: key, table: "Seed")
        }
        Kitchen.addToList(name("Хлеб Бородинский"), in: context)
        Kitchen.addToList(name("Лимон"), amount: 2, in: context)
        if let cheese = Kitchen.addToList(name("Сыр сливочный (крем-сыр)"), amount: 200, in: context) {
            Kitchen.setBought(cheese, true, in: context)
        }

        let recipes = (try? context.fetch(FetchDescriptor<Recipe>())) ?? []
        for (index, key) in ["Гречка с курицей", "Овощной салат", "Куриный суп с рисом"].enumerated() {
            guard let recipe = recipes.first(where: { $0.name == name(key) }) else { continue }
            let amounts = Dictionary(Kitchen.requirements(for: recipe, servings: recipe.baseServings)
                .map { ($0.productName, $0.amount) }, uniquingKeysWith: +)
            let log = Kitchen.cook(recipe: recipe, servings: recipe.baseServings, amounts: amounts, in: context)
            log.date = Calendar.current.date(byAdding: .hour, value: -20 * index - 2, to: .now) ?? .now
        }
        // Меню на четыре дня — из рецептов того набора, что загружен.
        let menu = recipes.filter { !$0.isCustom }.sorted { $0.name < $1.name }.prefix(6)
        for (index, recipe) in menu.enumerated() {
            let day = Calendar.current.date(byAdding: .day, value: index / 2, to: .now) ?? .now
            context.insert(PlannedMeal(day: day, recipe: recipe, servings: recipe.baseServings))
        }
        try? context.save()

        SharedLists.shared.showDemo(owner: String(localized: "Маша"), items: [
            (name("Помидоры"), 500, UnitCode.gram, false),
            (name("Яблоко"), 4, UnitCode.piece, false),
            (name("Огурцы"), 300, UnitCode.gram, true),
        ])
    }
}
