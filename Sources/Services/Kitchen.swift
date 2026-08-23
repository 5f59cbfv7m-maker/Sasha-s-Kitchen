import Foundation
import SwiftData

/// Операции над холодильником: пересчёт рецептов, списание при готовке,
/// пополнение остатков, сбор списка покупок.
enum Kitchen {

    // MARK: Остатки

    static func allStock(in context: ModelContext) -> [StockItem] {
        (try? context.fetch(FetchDescriptor<StockItem>())) ?? []
    }

    static func snapshot(of items: [StockItem]) -> StockSnapshot {
        var amounts: [String: Double] = [:]
        for item in items where item.product != nil {
            amounts[item.name, default: 0] += item.currentAmount
        }
        return StockSnapshot(amounts)
    }

    static func snapshot(in context: ModelContext) -> StockSnapshot {
        snapshot(of: allStock(in: context))
    }

    static func stockItem(for product: Product, in context: ModelContext) -> StockItem? {
        allStock(in: context).first { $0.product?.persistentModelID == product.persistentModelID }
    }

    // MARK: Рецепты

    static func requirements(for recipe: Recipe, servings: Int) -> [IngredientRequirement] {
        let factor = recipe.scale(for: servings)
        return recipe.orderedIngredients.map {
            IngredientRequirement(productName: $0.productName,
                                  amount: $0.amount(scaledBy: factor),
                                  unit: $0.unit)
        }
    }

    static func status(for recipe: Recipe, servings: Int,
                       stock: StockSnapshot) -> RecipeAvailability.Status {
        RecipeAvailability.status(for: requirements(for: recipe, servings: servings),
                                  stock: stock)
    }

    // MARK: Готовка

    /// Списывает фактически использованные количества и пишет запись в журнал.
    /// `amounts` — имя продукта → сколько реально ушло (в единице продукта).
    @discardableResult
    static func cook(recipe: Recipe, servings: Int, amounts: [String: Double],
                     in context: ModelContext) -> CookingLog {
        let stock = allStock(in: context)
        var used: [String: Double] = [:]
        var nutrition = Nutrition.zero

        for ingredient in recipe.orderedIngredients {
            guard let product = ingredient.product,
                  let requested = amounts[product.name], requested > 0 else { continue }
            let item = stock.first { $0.product?.persistentModelID == product.persistentModelID }
            // Списываем не больше, чем реально лежит: уйти в минус холодильник
            // не может, а журнал должен отражать списанное, а не запрошенное.
            let deducted = min(requested, item?.currentAmount ?? 0)
            item?.currentAmount = max(0, (item?.currentAmount ?? 0) - deducted)
            if deducted > 0 {
                used[product.name, default: 0] += deducted
                nutrition += product.nutrition(forAmount: deducted)
            }
        }

        let log = CookingLog(recipe: recipe, recipeName: recipe.name, servings: servings,
                             actualAmountsUsed: used, nutrition: nutrition)
        context.insert(log)
        try? context.save()
        return log
    }

    // MARK: Пополнение

    /// Докладывает продукт в холодильник и заново считает срок годности.
    static func replenish(_ item: StockItem, by amount: Double) {
        guard amount > 0 else { return }
        item.currentAmount += amount
        item.initialAmount = max(item.initialAmount, item.currentAmount)
        item.purchaseDate = .now
        if let days = item.product?.shelfLifeDays {
            item.expiryDate = Date.now.addingTimeInterval(Double(days) * 86_400)
        }
    }

    /// Ручная правка остатка — прогресс-бар считается от нового значения,
    /// если пользователь ввёл больше, чем было куплено.
    static func setAmount(_ item: StockItem, to amount: Double) {
        item.currentAmount = max(0, amount)
        item.initialAmount = max(item.initialAmount, item.currentAmount)
    }

    /// Сколько предложить купить: чтобы вышло примерно как в прошлый раз,
    /// округлённое до числа, с которым ходят в магазин, — 50 г, а не 42.
    static func suggestedPurchase(for item: StockItem) -> Double {
        let fallback = max((item.product?.lowStockThreshold ?? 0) * 3, 1)
        let base = item.initialAmount > 0 ? item.initialAmount : fallback
        let raw = max(base - item.currentAmount, base * 0.5)
        guard item.unit != "шт" else { return max(1, raw.rounded(.up)) }
        let step: Double = raw >= 400 ? 100 : (raw >= 100 ? 50 : 10)
        return max(step, (raw / step).rounded(.up) * step)
    }

    // MARK: Список покупок

    enum ShoppingState { case out, low }

    struct ShoppingEntry: Identifiable {
        let item: StockItem
        let state: ShoppingState
        var id: PersistentIdentifier { item.persistentModelID }
        var suggested: Double { Kitchen.suggestedPurchase(for: item) }
    }

    static func shoppingEntries(in context: ModelContext) -> [ShoppingEntry] {
        allStock(in: context)
            .filter { $0.product != nil && ($0.isEmpty || $0.isLow) }
            .map { ShoppingEntry(item: $0, state: $0.isEmpty ? .out : .low) }
            .sorted {
                if ($0.state == .out) != ($1.state == .out) { return $0.state == .out }
                return $0.item.name.localizedCaseInsensitiveCompare($1.item.name) == .orderedAscending
            }
    }

    /// Заголовок для напоминания и строки в чек-листе.
    static func purchaseTitle(for entry: ShoppingEntry) -> String {
        guard let product = entry.item.product else { return entry.item.name }
        return "\(entry.item.name) — \(product.amountLabel(entry.suggested))"
    }

    /// Текст для ShareLink: iMessage, Заметки, что угодно.
    static func shareText(for entries: [ShoppingEntry]) -> String {
        guard !entries.isEmpty else { return "Список покупок пуст — в холодильнике всё есть." }
        var lines = ["🛒 Купить (Оракул холодильника)", ""]
        let out = entries.filter { $0.state == .out }
        let low = entries.filter { $0.state == .low }
        if !out.isEmpty {
            lines.append("Закончилось:")
            lines += out.map { "• " + purchaseTitle(for: $0) }
            if !low.isEmpty { lines.append("") }
        }
        if !low.isEmpty {
            lines.append("На исходе:")
            lines += low.map { entry in
                let left = entry.item.product?.amountLabel(entry.item.currentAmount) ?? ""
                return "• \(purchaseTitle(for: entry)) (осталось \(left))"
            }
        }
        return lines.joined(separator: "\n")
    }
}

extension Kitchen.ShoppingState: Equatable {}
