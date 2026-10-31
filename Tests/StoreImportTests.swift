import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Импорт рецепта из магазина")
@MainActor
struct StoreImportTests {

    @Test("Ключ продукта — ровно как sk_normalize_key на сервере", arguments: [
        ("Яйцо", "яйцо"), ("  Молоко  ", "молоко"), ("Зелёный лук", "зеленый лук"),
        ("ЗЕЛЁНЫЙ  ЛУК", "зеленый лук"), ("Мисо   паста", "мисо паста"), ("Ёлка", "елка"),
        ("Chicken Breast", "chicken breast"), ("Мука\tпшеничная", "мука пшеничная"),
        ("томат\nчерри", "томат черри"), ("Сыр «Пармезан»", "сыр «пармезан»"),
    ])
    func keyMatchesServer(_ name: String, _ key: String) {
        #expect(StoreImport.key(name) == key)
    }

    private func bundle(_ products: [(name: String, unit: String, amount: Double)]) throws -> StoreBundle {
        let items = products.enumerated().map { index, p in
            """
            {"key": "\(StoreImport.key(p.name))", "name": "\(p.name)", "category": "Прочее", "unit": "\(p.unit)",
             "grams_per_unit": 55, "nutrition_per_100": {"kcal": 100, "protein": 1, "fat": 2, "carbs": 3},
             "low_stock_threshold": 0}
            """
        }
        let ingredients = products.enumerated().map { index, p in
            #"{"product_key": "\#(StoreImport.key(p.name))", "amount_per_base_serving": \#(p.amount), "unit": "\#(p.unit)", "position": \#(index)}"#
        }
        let json = """
        {"schema_version": 1,
         "recipe": {"source_id": "r-1", "title": "Pancakes", "steps": [{"order": 1, "text": "Mix."}],
                    "cook_time_minutes": 15, "prep_time_minutes": 5, "base_servings": 2,
                    "author": {"handle": "chef", "name": "Chef"}},
         "products": [\(items.joined(separator: ","))],
         "ingredients": [\(ingredients.joined(separator: ","))]}
        """
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(StoreBundle.self, from: Data(json.utf8))
    }

    @Test("Свои продукты находятся по ключу и через переводы; чужие создаются с КБЖУ; повтор не плодит копию")
    func importsRecipe() throws {
        let context = try makeContext(seeded: true)
        let before = try context.fetchCount(FetchDescriptor<Product>())
        let source = try bundle([("ЯЙЦА  куриные", "шт", 2), ("Wheat flour", "г", 200), ("Кленовый сироп", "мл", 40)])

        let recipe = StoreImport.apply(source, photo: nil, to: context)
        let names = recipe.orderedIngredients.map(\.productName)
        #expect(names == ["Яйца куриные", "Мука пшеничная", "Кленовый сироп"])
        #expect(recipe.cookingTimeMinutes == 20 && recipe.baseServings == 2 && recipe.isCustom)
        #expect(recipe.steps?.hasPrefix("1. Mix.") == true)
        #expect(try context.fetchCount(FetchDescriptor<Product>()) == before + 1)
        let syrup = try #require(recipe.orderedIngredients.last?.product)
        #expect(syrup.caloriesPer100 == 100 && syrup.unit == UnitCode.milliliter)

        // Тот же рецепт ещё раз — тот же объект, без дубликата.
        #expect(StoreImport.apply(source, photo: nil, to: context).persistentModelID == recipe.persistentModelID)
        #expect(try context.fetch(FetchDescriptor<Recipe>()).filter { $0.storeSourceID == "r-1" }.count == 1)
    }

    @Test("Граммы из магазина переводятся в штуки своего продукта")
    func convertsUnits() throws {
        let context = try makeContext(seeded: true)
        let recipe = StoreImport.apply(try bundle([("Яйца куриные", "г", 110)]), photo: nil, to: context)
        #expect(recipe.orderedIngredients.first?.amountPerBaseServing == 2)
    }
}
