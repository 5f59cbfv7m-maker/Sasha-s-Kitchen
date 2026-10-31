import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Импорт рецептов")
struct RecipeImportTests {

    @Test("Строки ингредиентов на разных языках", arguments: [
        ("2 cups flour", "flour", 480.0, UnitCode.milliliter),
        ("1 1/2 tsp salt", "salt", 7.5, UnitCode.milliliter),
        ("½ lb ground beef", "ground beef", 226.8, UnitCode.gram),
        ("3 eggs", "eggs", 3.0, UnitCode.piece),
        ("2 cloves garlic, minced", "garlic", 2.0, UnitCode.piece),
        ("200 г муки", "муки", 200.0, UnitCode.gram),
        ("Молоко — 500 мл", "Молоко", 500.0, UnitCode.milliliter),
        ("2 ст. л. сахара", "сахара", 30.0, UnitCode.milliliter),
        ("卵 2個", "卵", 2.0, UnitCode.piece),
        ("醤油 大さじ2", "醤油", 30.0, UnitCode.milliliter),
        ("1,5 kg Kartoffeln", "Kartoffeln", 1500.0, UnitCode.gram),
    ])
    func parsesIngredient(_ line: String, _ name: String, _ amount: Double, _ unit: String) {
        let parsed = RecipeImport.parseIngredient(line)
        #expect(parsed.name == name)
        #expect(abs(parsed.amount - amount) < 0.11)
        #expect(parsed.unit == unit)
    }

    @Test("Без количества — имя остаётся, количество впишет человек")
    func noQuantity() {
        let parsed = RecipeImport.parseIngredient("Salt and pepper to taste")
        #expect(parsed.amount == 0 && parsed.name == "Salt and pepper to taste")
    }

    @Test("JSON-LD schema.org Recipe со страницы: имя, порции, время, ингредиенты, шаги, фото")
    func parsesJSONLD() throws {
        let html = """
        <html><head><script type="application/ld+json">{"@context":"https://schema.org","@graph":[
          {"@type":"WebPage","name":"x"},
          {"@type":["Recipe"],"name":"Classic Pancakes &amp; Syrup","recipeYield":["4","4 servings"],
           "totalTime":"PT1H5M","image":{"url":"https://example.com/p.jpg"},
           "recipeIngredient":["2 cups flour","2 eggs","1 cup milk"],
           "recipeInstructions":[{"@type":"HowToStep","text":"Mix."},{"@type":"HowToStep","text":"Fry."}]}]}
        </script></head></html>
        """
        let recipe = try #require(RecipeImport.parseHTML(html))
        #expect(recipe.name == "Classic Pancakes & Syrup")
        #expect(recipe.servings == 4 && recipe.minutes == 65)
        #expect(recipe.ingredients.map(\.name) == ["flour", "eggs", "milk"])
        #expect(recipe.steps == "1. Mix.\n2. Fry.")
        #expect(recipe.imageURL?.absoluteString == "https://example.com/p.jpg")
        #expect(RecipeImport.parseHTML("<html>no recipe</html>") == nil)
    }

    @Test("Текст с фото: название, ингредиенты с количеством, остальное — шаги")
    func parsesRecognizedText() throws {
        let recipe = try #require(RecipeImport.fromText(["Блины", "Мука 200 г", "Молоко 500 мл", "Яйца 2 шт",
                                                         "Смешать всё и жарить на сковороде."]))
        #expect(recipe.name == "Блины")
        #expect(recipe.ingredients.map(\.name) == ["Мука", "Молоко", "Яйца"])
        #expect(recipe.steps == "Смешать всё и жарить на сковороде.")
    }

    @Test("Ингредиент сопоставляется с каталогом; новый — создаётся без КБЖУ")
    @MainActor
    func resolvesProducts() throws {
        let context = try makeContext(seeded: true)
        let milk = RecipeImport.product(for: ParsedIngredient(name: "молоко", amount: 200, unit: UnitCode.milliliter), in: context)
        #expect(milk.product.name == "Молоко 2.5%" && !milk.isNew && milk.amount == 200)
        let eggs = RecipeImport.product(for: ParsedIngredient(name: "Яйца куриные", amount: 110, unit: UnitCode.gram), in: context)
        #expect(eggs.amount == 2)
        let saffron = RecipeImport.product(for: ParsedIngredient(name: "шафран", amount: 1, unit: UnitCode.gram), in: context)
        #expect(saffron.isNew && saffron.product.name == "Шафран" && saffron.product.caloriesPer100 == 0)
    }
}
