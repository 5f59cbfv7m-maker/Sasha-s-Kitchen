import Foundation
import SwiftData
import Testing
@testable import FridgeOracle

@Suite("Стартовые наборы по регионам")
@MainActor
struct SeedSetTests {

    @Test("В каждом наборе рецепты ссылаются только на свои продукты, имена уникальны", arguments: SeedSet.allCases)
    func setIsConsistent(_ set: SeedSet) {
        let names = set.products.map(\.name)
        #expect(Set(names).count == names.count)
        #expect(Set(set.recipes.map(\.name)).count == set.recipes.count)
        #expect(set.recipes.count >= 18 && set.products.count >= 25)
        for recipe in set.recipes {
            for ingredient in recipe.ingredients {
                #expect(names.contains(ingredient.product), "\(set): «\(recipe.name)» → «\(ingredient.product)»")
            }
        }
    }

    @Test("Набор выбирается по региону устройства")
    func regionPicksSet() {
        #expect(SeedSet.forRegion(Locale.Region("RU")) == .russia)
        #expect(SeedSet.forRegion(Locale.Region("KZ")) == .russia)
        #expect(SeedSet.forRegion(Locale.Region("JP")) == .eastAsia)
        #expect(SeedSet.forRegion(Locale.Region("KR")) == .eastAsia)
        #expect(SeedSet.forRegion(Locale.Region("TH")) == .thailand)
        #expect(SeedSet.forRegion(Locale.Region("US")) == .west)
        #expect(SeedSet.forRegion(Locale.Region("DE")) == .west)
        #expect(SeedSet.forRegion(nil) == .west)
    }

    @Test("Набор раскладывается в холодильник: часть рецептов готова, список покупок не пуст", arguments: SeedSet.allCases)
    func setLoads(_ set: SeedSet) throws {
        let context = try makeContext()
        SeedLoader.load(into: context, localized: false, set: set)
        #expect(try context.fetchCount(FetchDescriptor<Product>()) == set.products.count)
        let recipes = try context.fetch(FetchDescriptor<Recipe>())
        #expect(recipes.count == set.recipes.count)
        let snapshot = Kitchen.snapshot(in: context)
        let ready = recipes.filter { Kitchen.status(for: $0, servings: $0.baseServings, stock: snapshot).isReady }
        #expect(!ready.isEmpty)
        #expect(!Kitchen.shoppingEntries(in: context).isEmpty)
    }
}
