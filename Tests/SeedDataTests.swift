import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Стартовые данные")
struct SeedDataTests {

    @Test("49 продуктов и 40 рецептов из starter-data.md")
    func counts() throws {
        #expect(SeedData.products.count == 49)
        #expect(SeedData.recipes.count == 40)

        let context = try makeContext(seeded: true)
        #expect(try context.fetchCount(FetchDescriptor<Product>()) == 49)
        #expect(try context.fetchCount(FetchDescriptor<Recipe>()) == 40)
        #expect(try context.fetchCount(FetchDescriptor<StockItem>()) == 49)
    }

    @Test("Каждый ингредиент рецепта ссылается на продукт из каталога")
    func ingredientsResolve() throws {
        let catalog = Set(SeedData.products.map(\.name))
        for recipe in SeedData.recipes {
            #expect(!recipe.ingredients.isEmpty, "\(recipe.name): пустой состав")
            for ingredient in recipe.ingredients {
                #expect(catalog.contains(ingredient.product),
                        "\(recipe.name): нет продукта «\(ingredient.product)»")
            }
        }
    }

    @Test("Имена продуктов уникальны — на них завязаны остатки и рецепты")
    func namesAreUnique() {
        #expect(Set(SeedData.products.map(\.name)).count == SeedData.products.count)
        #expect(Set(SeedData.recipes.map(\.name)).count == SeedData.recipes.count)
    }

    @Test("Связи рецепт → ингредиент → продукт живы после загрузки в SwiftData")
    func relationshipsSurviveLoad() throws {
        let context = try makeContext(seeded: true)
        let recipes = try context.fetch(FetchDescriptor<Recipe>())
        for recipe in recipes {
            #expect(!recipe.ingredients.isEmpty, "\(recipe.name): состав потерялся")
            for ingredient in recipe.ingredients {
                #expect(ingredient.product != nil,
                        "\(recipe.name): ингредиент без продукта")
                #expect(ingredient.amountPerBaseServing > 0)
            }
        }
    }

    @Test("Порядок ингредиентов совпадает с исходным файлом")
    func ingredientOrderPreserved() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        #expect(omelette.orderedIngredients.map(\.productName)
                == ["Яйца куриные", "Молоко 2.5%", "Сливочное масло", "Соль"])
    }

    @Test("Калорийность порции в разумных пределах для всех 40 рецептов")
    func caloriesAreSane() throws {
        // Оценки «~X ккал/порция» в starter-data.md сделаны на глаз и местами
        // расходятся между собой, поэтому проверяем не их, а порядок величины
        // расчёта: пустых и абсурдных блюд быть не должно.
        let context = try makeContext(seeded: true)
        for recipe in try context.fetch(FetchDescriptor<Recipe>()) {
            let perServing = recipe.nutritionPerServing(for: recipe.baseServings).calories
            #expect(perServing > 40, "\(recipe.name): \(perServing) ккал на порцию — подозрительно мало")
            #expect(perServing < 1_200, "\(recipe.name): \(perServing) ккал на порцию — подозрительно много")
        }
    }

    @Test("Повторная загрузка не дублирует каталог")
    func loadIsIdempotent() throws {
        let context = try makeContext(seeded: true)
        #expect(SeedLoader.loadIfNeeded(into: context) == false)
        #expect(try context.fetchCount(FetchDescriptor<Product>()) == 49)
    }

    @Test("Стартовый холодильник даёт непустой список покупок и готовые рецепты")
    func startingStateIsUseful() throws {
        let context = try makeContext(seeded: true)
        let shopping = Kitchen.shoppingEntries(in: context)
        #expect(!shopping.isEmpty, "вкладке «Для покупки» нечего показать на старте")

        let snapshot = Kitchen.snapshot(in: context)
        let ready = try context.fetch(FetchDescriptor<Recipe>()).filter {
            Kitchen.status(for: $0, servings: $0.baseServings, stock: snapshot).isReady
        }
        #expect(!ready.isEmpty, "ни один рецепт не собирается из стартовых остатков")
        #expect(ready.count < 40, "фильтр «могу приготовить» ничего не отсекает")
    }
}
