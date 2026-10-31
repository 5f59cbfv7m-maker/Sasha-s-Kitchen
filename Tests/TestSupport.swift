import Foundation
import SwiftData
@testable import FridgeOracle

/// Каждый тест получает свою базу в памяти — на диск ничего не попадает.
@MainActor
func makeContext(seeded: Bool = false) throws -> ModelContext {
    let container = try ModelContainer(
        for: FridgeOracleApp.schema,
        configurations: ModelConfiguration(isStoredInMemoryOnly: true, cloudKitDatabase: .none))
    let context = ModelContext(container)
    // Тесты сверяются с русскими именами, на каком бы языке ни был симулятор.
    if seeded { SeedLoader.load(into: context, localized: false) }
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
