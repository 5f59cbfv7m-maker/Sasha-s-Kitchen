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

    /// Срок годности через календарь, а не `86 400 × дни`: переход на летнее
    /// время иначе сдвигает дату на час, а то и на день.
    static func expiry(afterDays days: Int, from date: Date = .now) -> Date {
        Calendar.current.date(byAdding: .day, value: days, to: date) ?? date
    }

    // MARK: Рецепты

    static func requirements(for recipe: Recipe, servings: Int) -> [IngredientRequirement] {
        let factor = recipe.scale(for: servings)
        // Ингредиент без продукта (продукт удалили) рецепту не нужен: иначе
        // рецепт навсегда «не готов» из-за строки «—».
        return recipe.orderedIngredients.compactMap {
            guard let product = $0.product else { return nil }
            return IngredientRequirement(productName: product.name,
                                         amount: $0.amount(scaledBy: factor),
                                         unit: product.unit)
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
        MealPlan.markCooked(recipe, in: context)
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
            item.expiryDate = expiry(afterDays: days)
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
        guard item.unit != UnitCode.piece else { return max(1, raw.rounded(.up)) }
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
        shoppingEntries(from: allStock(in: context))
    }

    /// Общий отбор для вкладки, её счётчика и тестов.
    static func shoppingEntries(from stock: [StockItem]) -> [ShoppingEntry] {
        stock
            .filter { $0.product != nil && ($0.isEmpty || $0.isLow) }
            .map { ShoppingEntry(item: $0, state: $0.isEmpty ? .out : .low) }
            .sorted {
                if ($0.state == .out) != ($1.state == .out) { return $0.state == .out }
                return $0.item.name.localizedStandardCompare($1.item.name) == .orderedAscending
            }
    }

    /// Заголовок для напоминания и строки в чек-листе.
    static func purchaseTitle(for entry: ShoppingEntry) -> String {
        guard let product = entry.item.product else { return entry.item.name }
        return "\(entry.item.name) — \(product.amountLabel(entry.suggested))"
    }

    static func purchaseTitle(for item: ShoppingItem) -> String {
        guard item.amount > 0 else { return item.name }
        let amount = item.product?.amountLabel(item.amount) ?? Fmt.number(item.amount)
        return "\(item.name) — \(amount)"
    }

    /// Текст для ShareLink: iMessage, Заметки, что угодно.
    static func shareText(for entries: [ShoppingEntry], extra: [ShoppingItem] = []) -> String {
        let extra = extra.filter { !$0.isBought }
        guard !entries.isEmpty || !extra.isEmpty else {
            return String(localized: "Список покупок пуст — в холодильнике всё есть.")
        }
        var lines = [String(localized: "🛒 Купить (Sasha’s Kitchen)")]
        let out = entries.filter { $0.state == .out }
        let low = entries.filter { $0.state == .low }
        if !extra.isEmpty {
            lines += ["", String(localized: "Мой список:")]
            lines += extra.map { "• " + purchaseTitle(for: $0) }
        }
        if !out.isEmpty {
            lines += ["", String(localized: "Закончилось:")]
            lines += out.map { "• " + purchaseTitle(for: $0) }
        }
        if !low.isEmpty {
            lines += ["", String(localized: "На исходе:")]
            lines += low.map { entry in
                let left = entry.item.product?.amountLabel(entry.item.currentAmount) ?? ""
                return "• " + String(localized: "\(purchaseTitle(for: entry)) (осталось \(left))")
            }
        }
        return lines.joined(separator: "\n")
    }

    // MARK: Свой список

    /// Вписывает позицию в список. Имя, совпавшее с продуктом каталога,
    /// привязывает её к продукту — тогда «Купил» пополнит остаток. Повторная
    /// позиция того же продукта не плодится, а докладывает количество.
    @discardableResult
    static func addToList(_ name: String, amount: Double = 0,
                          in context: ModelContext) -> ShoppingItem? {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return nil }
        let products = (try? context.fetch(FetchDescriptor<Product>())) ?? []
        let product = products.first { $0.name.sameName(as: name) }
        let items = (try? context.fetch(FetchDescriptor<ShoppingItem>())) ?? []
        if let existing = items.first(where: { !$0.isBought && $0.name.sameName(as: product?.name ?? name) }) {
            existing.amount += max(0, amount)
            try? context.save()
            return existing
        }
        let item = ShoppingItem(name: product?.name ?? name, amount: max(0, amount), product: product)
        context.insert(item)
        try? context.save()
        return item
    }

    /// Недостающее для рецепта — в список, с точным количеством нехватки.
    static func addMissing(_ shortages: [Shortage], in context: ModelContext) {
        for shortage in shortages {
            addToList(shortage.productName, amount: shortage.missing.roundedUp(for: shortage.unit),
                      in: context)
        }
    }

    /// Отметка «купил». Для продукта из каталога остаток пополняется сразу, а
    /// снятая отметка возвращает его обратно — ошибочное нажатие не портит
    /// холодильник.
    static func setBought(_ item: ShoppingItem, _ bought: Bool, in context: ModelContext) {
        guard item.isBought != bought else { return }
        item.isBought = bought
        if let product = item.product, let stock = product.stockItems?.first {
            let amount = item.amount > 0 ? item.amount : suggestedPurchase(for: stock)
            if item.amount == 0 { item.amount = amount }
            if bought {
                replenish(stock, by: amount)
            } else {
                stock.currentAmount = max(0, stock.currentAmount - amount)
            }
        }
        try? context.save()
    }

    static func removeBought(in context: ModelContext) {
        let items = (try? context.fetch(FetchDescriptor<ShoppingItem>())) ?? []
        for item in items where item.isBought { context.delete(item) }
        try? context.save()
    }

    /// «Купил всё» после похода в магазин: каждое предложенное количество — в холодильник.
    static func buyAll(_ entries: [ShoppingEntry], in context: ModelContext) {
        for entry in entries { replenish(entry.item, by: entry.suggested) }
        try? context.save()
    }
}

nonisolated extension String {
    /// Совпадение имён продуктов без учёта регистра, диакритики и ширины:
    /// «Молоко» = «молоко», «Café» = «cafe», «ｍｉｌｋ» = «milk».
    func sameName(as other: String) -> Bool {
        compare(other, options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive])
            == .orderedSame
    }
}

nonisolated extension Double {
    /// Нехватку округляем вверх: 0,3 яйца — это одно яйцо, 42,5 г — 43 г.
    func roundedUp(for unit: String) -> Double {
        unit == UnitCode.piece ? max(1, rounded(.up)) : rounded(.up)
    }
}

extension Kitchen.ShoppingState: Equatable {}
