import Foundation
import SwiftData
@testable import FridgeOracle

/// Каждый тест получает свою базу в памяти — на диск ничего не попадает.
@MainActor
func makeContext(seeded: Bool = false) throws -> ModelContext {
    let container = try ModelContainer(
        for: Product.self, StockItem.self, Recipe.self,
        RecipeIngredient.self, CookingLog.self,
        configurations: ModelConfiguration(isStoredInMemoryOnly: true))
    let context = ModelContext(container)
    if seeded { SeedLoader.load(into: context) }
    return context
}

@MainActor
func product(_ name: String, in context: ModelContext) throws -> Product {
    let all = try context.fetch(FetchDescriptor<Product>())
    guard let match = all.first(where: { $0.name == name }) else {
        throw TestFailure.missing(name)
    }
    return match
}

@MainActor
func recipe(_ name: String, in context: ModelContext) throws -> Recipe {
    let all = try context.fetch(FetchDescriptor<Recipe>())
    guard let match = all.first(where: { $0.name == name }) else {
        throw TestFailure.missing(name)
    }
    return match
}

@MainActor
func stock(_ name: String, in context: ModelContext) throws -> StockItem {
    guard let item = Kitchen.allStock(in: context).first(where: { $0.name == name }) else {
        throw TestFailure.missing(name)
    }
    return item
}

enum TestFailure: Error { case missing(String) }
