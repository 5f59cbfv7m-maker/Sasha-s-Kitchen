import CryptoKit
import Foundation
import SwiftData

/// Что видит приглашённый в семейную кухню.
nonisolated enum KitchenAccess: String, Codable, CaseIterable, Sendable {
    /// Продукты и остатки: у гостя — холодильник семьи, и только он.
    case fridge
    /// Вся кухня: холодильник, рецепты, список, журнал, меню.
    case everything
}

/// Поле записи кухни. Хранится и сравнивается в виде JSON, поэтому в CloudKit
/// запись — одно поле `payload` (и фото рецепта отдельным файлом).
nonisolated enum MirrorValue: Codable, Hashable, Sendable {
    case text(String), number(Double), date(Date), flag(Bool), none
}

nonisolated struct MirrorRecord: Codable, Hashable, Sendable {
    var type: String
    var uid: String
    var fields: [String: MirrorValue]

    /// Имя записи CloudKit: тип и uid — uid уникален только внутри типа.
    var recordName: String { type + "-" + uid }
}

/// Кухня SwiftData ↔ записи для общей зоны. Одна функция выгружает всё, что
/// разрешено доступом, другая вносит пришедшее; отправляется только то, что
/// отличается от отправленного в прошлый раз (`diff`), — поэтому применённое
/// чужое изменение не уходит обратно эхом.
enum KitchenMirror {

    static func types(for access: KitchenAccess) -> [String] {
        switch access {
        case .fridge: ["Product", "StockItem"]
        case .everything: ["Product", "StockItem", "Recipe", "RecipeIngredient", "ShoppingItem", "CookingLog", "PlannedMeal"]
        }
    }

    // MARK: Выгрузка

    static func export(_ context: ModelContext, access: KitchenAccess) -> [MirrorRecord] {
        func all<T: PersistentModel>(_ type: T.Type) -> [T] { (try? context.fetch(FetchDescriptor<T>())) ?? [] }
        func optional(_ text: String?) -> MirrorValue { text.map { .text($0) } ?? .none }
        func optional(_ date: Date?) -> MirrorValue { date.map { .date($0) } ?? .none }
        let allowed = Set(types(for: access))
        var records: [MirrorRecord] = []

        for product in all(Product.self) where !product.uid.isEmpty {
            records.append(MirrorRecord(type: "Product", uid: product.uid, fields: [
                "name": .text(product.name), "category": .text(product.category), "unit": .text(product.unit),
                "gramsPerUnit": .number(product.gramsPerUnit), "kcal": .number(product.caloriesPer100),
                "protein": .number(product.proteinPer100), "fat": .number(product.fatPer100),
                "carbs": .number(product.carbsPer100), "low": .number(product.lowStockThreshold),
                "shelfLife": product.shelfLifeDays.map { .number(Double($0)) } ?? .none,
                "barcode": optional(product.barcode), "isCustom": .flag(product.isCustom),
            ]))
        }
        for item in all(StockItem.self) where !item.uid.isEmpty {
            guard let product = item.product else { continue }
            records.append(MirrorRecord(type: "StockItem", uid: item.uid, fields: [
                "product": .text(product.uid), "current": .number(item.currentAmount),
                "initial": .number(item.initialAmount), "purchase": .date(item.purchaseDate),
                "expiry": optional(item.expiryDate),
            ]))
        }
        guard allowed.contains("Recipe") else { return records }

        for recipe in all(Recipe.self) where !recipe.uid.isEmpty {
            records.append(MirrorRecord(type: "Recipe", uid: recipe.uid, fields: [
                "name": .text(recipe.name), "minutes": .number(Double(recipe.cookingTimeMinutes)),
                "servings": .number(Double(recipe.baseServings)), "steps": optional(recipe.steps),
                "isCustom": .flag(recipe.isCustom), "createdAt": .date(recipe.createdAt),
                "photo": optional(recipe.photoData.map(photoHash)),
            ]))
        }
        for ingredient in all(RecipeIngredient.self) where !ingredient.uid.isEmpty {
            guard let recipe = ingredient.recipe, let product = ingredient.product else { continue }
            records.append(MirrorRecord(type: "RecipeIngredient", uid: ingredient.uid, fields: [
                "recipe": .text(recipe.uid), "product": .text(product.uid),
                "amount": .number(ingredient.amountPerBaseServing), "order": .number(Double(ingredient.order)),
            ]))
        }
        for item in all(ShoppingItem.self) where !item.uid.isEmpty {
            records.append(MirrorRecord(type: "ShoppingItem", uid: item.uid, fields: [
                "name": .text(item.name), "amount": .number(item.amount), "product": optional(item.product?.uid),
                "isBought": .flag(item.isBought), "createdAt": .date(item.createdAt),
            ]))
        }
        for log in all(CookingLog.self) where !log.uid.isEmpty {
            // Ключи по порядку: иначе один и тот же журнал давал бы разный текст
            // и уходил бы на сервер при каждой сверке.
            let encoder = JSONEncoder()
            encoder.outputFormatting = .sortedKeys
            let used = (try? encoder.encode(log.actualAmountsUsed)).flatMap { String(data: $0, encoding: .utf8) }
            records.append(MirrorRecord(type: "CookingLog", uid: log.uid, fields: [
                "recipeName": .text(log.recipeName), "recipe": optional(log.recipe?.uid), "date": .date(log.date),
                "servings": .number(Double(log.servings)), "kcal": .number(log.calories),
                "protein": .number(log.protein), "fat": .number(log.fat), "carbs": .number(log.carbs),
                "used": optional(used),
            ]))
        }
        for meal in all(PlannedMeal.self) where !meal.uid.isEmpty {
            guard let recipe = meal.recipe else { continue }
            records.append(MirrorRecord(type: "PlannedMeal", uid: meal.uid, fields: [
                "day": .date(meal.day), "recipe": .text(recipe.uid), "servings": .number(Double(meal.servings)),
                "isCooked": .flag(meal.isCooked), "createdAt": .date(meal.createdAt),
            ]))
        }
        return records
    }

    static func photoHash(_ data: Data) -> String {
        SHA256.hash(data: data).prefix(12).map { String(format: "%02x", $0) }.joined()
    }

    /// Что отправить и что удалить: сравнение с тем, что уже на сервере.
    nonisolated static func diff(current: [MirrorRecord], sent: [String: MirrorRecord])
        -> (save: [MirrorRecord], delete: [String]) {
        let names = Set(current.map(\.recordName))
        return (current.filter { sent[$0.recordName] != $0 }, sent.keys.filter { !names.contains($0) }.sorted())
    }

    // MARK: Внесение

    /// Пришедшие записи — в базу: найти по uid или создать, связи — по uid.
    /// Сначала продукты и рецепты, потом то, что на них ссылается.
    static func apply(_ records: [MirrorRecord], photos: [String: Data] = [:], deleted: [String] = [],
                      to context: ModelContext) {
        func all<T: PersistentModel>(_ type: T.Type) -> [T] { (try? context.fetch(FetchDescriptor<T>())) ?? [] }
        var products = Dictionary(all(Product.self).map { ($0.uid, $0) }, uniquingKeysWith: { first, _ in first })
        var recipes = Dictionary(all(Recipe.self).map { ($0.uid, $0) }, uniquingKeysWith: { first, _ in first })
        let byType = Dictionary(grouping: records, by: \.type)

        func text(_ value: MirrorValue?) -> String? { if case .text(let text) = value { text } else { nil } }
        func number(_ value: MirrorValue?) -> Double? { if case .number(let number) = value { number } else { nil } }
        func date(_ value: MirrorValue?) -> Date? { if case .date(let date) = value { date } else { nil } }
        func flag(_ value: MirrorValue?) -> Bool { if case .flag(let flag) = value { flag } else { false } }

        for record in byType["Product"] ?? [] {
            let f = record.fields
            let product = products[record.uid] ?? {
                let created = Product(name: "", category: "", unit: UnitCode.gram, caloriesPer100: 0,
                                      proteinPer100: 0, fatPer100: 0, carbsPer100: 0)
                created.uid = record.uid
                context.insert(created)
                products[record.uid] = created
                return created
            }()
            product.name = text(f["name"]) ?? product.name
            product.category = text(f["category"]) ?? product.category
            product.unit = text(f["unit"]) ?? product.unit
            product.gramsPerUnit = number(f["gramsPerUnit"]) ?? 1
            product.caloriesPer100 = number(f["kcal"]) ?? 0
            product.proteinPer100 = number(f["protein"]) ?? 0
            product.fatPer100 = number(f["fat"]) ?? 0
            product.carbsPer100 = number(f["carbs"]) ?? 0
            product.lowStockThreshold = number(f["low"]) ?? 0
            product.shelfLifeDays = number(f["shelfLife"]).map { Int($0) }
            product.barcode = text(f["barcode"])
            product.isCustom = flag(f["isCustom"])
        }
        for record in byType["Recipe"] ?? [] {
            let f = record.fields
            let recipe = recipes[record.uid] ?? {
                let created = Recipe(name: "", cookingTimeMinutes: 0, baseServings: 1)
                created.uid = record.uid
                context.insert(created)
                recipes[record.uid] = created
                return created
            }()
            recipe.name = text(f["name"]) ?? recipe.name
            recipe.cookingTimeMinutes = Int(number(f["minutes"]) ?? 0)
            recipe.baseServings = max(1, Int(number(f["servings"]) ?? 1))
            recipe.steps = text(f["steps"])
            recipe.isCustom = flag(f["isCustom"])
            recipe.createdAt = date(f["createdAt"]) ?? recipe.createdAt
            if text(f["photo"]) == nil {
                recipe.photoData = nil
            } else if let photo = photos[record.recordName] {
                recipe.photoData = photo
            }
        }

        func upsert<T: PersistentModel>(_ type: String, _ existing: [T], uid: ReferenceWritableKeyPath<T, String>,
                                        make: () -> T?, update: (T, [String: MirrorValue]) -> Void) {
            var byUID = Dictionary(existing.map { ($0[keyPath: uid], $0) }, uniquingKeysWith: { first, _ in first })
            for record in byType[type] ?? [] {
                let object = byUID[record.uid] ?? make()
                guard let object else { continue }
                if byUID[record.uid] == nil {
                    // Новый объект — с присланным uid, иначе следующая порция
                    // его не узнала бы и завела второй.
                    object[keyPath: uid] = record.uid
                    context.insert(object)
                    byUID[record.uid] = object
                }
                update(object, record.fields)
            }
        }

        // Остатки, ингредиенты и блюда меню создаются до обновления полей: им
        // нужны продукт и рецепт сразу, в инициализаторе, а остальное допишет upsert.
        for record in byType["StockItem"] ?? [] where !all(StockItem.self).contains(where: { $0.uid == record.uid }) {
            guard let product = text(record.fields["product"]).flatMap({ products[$0] }) else { continue }
            let item = StockItem(product: product, currentAmount: number(record.fields["current"]) ?? 0,
                                 initialAmount: number(record.fields["initial"]),
                                 purchaseDate: date(record.fields["purchase"]) ?? .now,
                                 expiryDate: date(record.fields["expiry"]))
            item.uid = record.uid
            context.insert(item)
        }
        for record in byType["RecipeIngredient"] ?? [] where !all(RecipeIngredient.self).contains(where: { $0.uid == record.uid }) {
            guard let product = text(record.fields["product"]).flatMap({ products[$0] }),
                  let recipe = text(record.fields["recipe"]).flatMap({ recipes[$0] }) else { continue }
            let ingredient = RecipeIngredient(product: product, amountPerBaseServing: number(record.fields["amount"]) ?? 0,
                                              order: Int(number(record.fields["order"]) ?? 0))
            ingredient.uid = record.uid
            context.insert(ingredient)
            ingredient.recipe = recipe
        }
        for record in byType["PlannedMeal"] ?? [] where !all(PlannedMeal.self).contains(where: { $0.uid == record.uid }) {
            guard let recipe = text(record.fields["recipe"]).flatMap({ recipes[$0] }),
                  let day = date(record.fields["day"]) else { continue }
            let meal = PlannedMeal(day: day, recipe: recipe, servings: Int(number(record.fields["servings"]) ?? 1))
            meal.uid = record.uid
            meal.isCooked = flag(record.fields["isCooked"])
            context.insert(meal)
        }

        upsert("StockItem", all(StockItem.self), uid: \.uid, make: { nil }) { item, f in
            item.product = text(f["product"]).flatMap { products[$0] }
            item.currentAmount = number(f["current"]) ?? 0
            item.initialAmount = number(f["initial"]) ?? 0
            item.purchaseDate = date(f["purchase"]) ?? item.purchaseDate
            item.expiryDate = date(f["expiry"])
        }
        upsert("RecipeIngredient", all(RecipeIngredient.self), uid: \.uid, make: { nil }) { ingredient, f in
            ingredient.recipe = text(f["recipe"]).flatMap { recipes[$0] }
            ingredient.product = text(f["product"]).flatMap { products[$0] }
            ingredient.amountPerBaseServing = number(f["amount"]) ?? 0
            ingredient.order = Int(number(f["order"]) ?? 0)
        }
        upsert("ShoppingItem", all(ShoppingItem.self), uid: \.uid, make: { ShoppingItem(name: "") }) { item, f in
            item.name = text(f["name"]) ?? item.name
            item.amount = number(f["amount"]) ?? 0
            item.product = text(f["product"]).flatMap { products[$0] }
            item.isBought = flag(f["isBought"])
            item.createdAt = date(f["createdAt"]) ?? item.createdAt
        }
        upsert("CookingLog", all(CookingLog.self), uid: \.uid,
               make: { CookingLog(recipe: nil, recipeName: "", servings: 1, actualAmountsUsed: [:], nutrition: .zero) }) { log, f in
            log.recipeName = text(f["recipeName"]) ?? log.recipeName
            log.recipe = text(f["recipe"]).flatMap { recipes[$0] }
            log.date = date(f["date"]) ?? log.date
            log.servings = Int(number(f["servings"]) ?? 1)
            log.calories = number(f["kcal"]) ?? 0
            log.protein = number(f["protein"]) ?? 0
            log.fat = number(f["fat"]) ?? 0
            log.carbs = number(f["carbs"]) ?? 0
            log.actualAmountsUsed = text(f["used"]).flatMap {
                try? JSONDecoder().decode([String: Double].self, from: Data($0.utf8))
            } ?? [:]
        }
        upsert("PlannedMeal", all(PlannedMeal.self), uid: \.uid, make: { nil }) { meal, f in
            meal.day = date(f["day"]) ?? meal.day
            meal.recipe = text(f["recipe"]).flatMap { recipes[$0] }
            meal.servings = Int(number(f["servings"]) ?? 1)
            meal.isCooked = flag(f["isCooked"])
            meal.createdAt = date(f["createdAt"]) ?? meal.createdAt
        }

        delete(deleted, in: context)
        try? context.save()
    }

    private static func delete(_ recordNames: [String], in context: ModelContext) {
        func remove<T: PersistentModel>(_ type: T.Type, _ uid: KeyPath<T, String>, _ uids: Set<String>) {
            for object in (try? context.fetch(FetchDescriptor<T>())) ?? [] where uids.contains(object[keyPath: uid]) {
                context.delete(object)
            }
        }
        var byType: [String: Set<String>] = [:]
        for name in recordNames {
            guard let dash = name.firstIndex(of: "-") else { continue }
            byType[String(name[..<dash]), default: []].insert(String(name[name.index(after: dash)...]))
        }
        remove(Product.self, \.uid, byType["Product"] ?? [])
        remove(StockItem.self, \.uid, byType["StockItem"] ?? [])
        remove(Recipe.self, \.uid, byType["Recipe"] ?? [])
        remove(RecipeIngredient.self, \.uid, byType["RecipeIngredient"] ?? [])
        remove(ShoppingItem.self, \.uid, byType["ShoppingItem"] ?? [])
        remove(CookingLog.self, \.uid, byType["CookingLog"] ?? [])
        remove(PlannedMeal.self, \.uid, byType["PlannedMeal"] ?? [])
    }
}
