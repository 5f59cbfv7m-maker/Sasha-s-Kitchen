import Foundation
import SwiftData

/// iCloud сводит вместе базы нескольких устройств. Если одноимённый продукт
/// завели на двух устройствах до синхронизации (или оба загрузили стартовый
/// набор), продуктов станет два, а имя — ключ, на котором сходятся рецепты и
/// остатки. Здесь дубликаты сливаются в один. «Выживший» — с наименьшим `uid`:
/// каждое устройство выбирает одного и того же, и базы сходятся.
enum SyncCleanup {

    static func run(in context: ModelContext) {
        let products = (try? context.fetch(FetchDescriptor<Product>())) ?? []
        let recipes = (try? context.fetch(FetchDescriptor<Recipe>())) ?? []
        var changed = false

        // Записи из версии без uid получают его на первом запуске новой версии.
        for product in products where product.uid.isEmpty {
            product.uid = UUID().uuidString
            changed = true
        }
        for recipe in recipes where recipe.uid.isEmpty {
            recipe.uid = UUID().uuidString
            changed = true
        }

        for group in Dictionary(grouping: products, by: \.name).values where group.count > 1 {
            let sorted = group.sorted { $0.uid < $1.uid }
            let keeper = sorted[0]
            for duplicate in sorted.dropFirst() {
                merge(duplicate, into: keeper)
                context.delete(duplicate)
            }
            changed = true
        }

        // Свои рецепты могут законно называться одинаково — сливаем только стартовые.
        let seeded = recipes.filter { !$0.isCustom }
        for group in Dictionary(grouping: seeded, by: \.name).values where group.count > 1 {
            let sorted = group.sorted { $0.uid < $1.uid }
            for duplicate in sorted.dropFirst() {
                for log in duplicate.logs ?? [] { log.recipe = sorted[0] }
                context.delete(duplicate)
            }
            changed = true
        }

        if changed { try? context.save() }
    }

    private static func merge(_ duplicate: Product, into keeper: Product) {
        // Остаток у продукта один: свой остаток «выжившего» важнее.
        if keeper.stockItems?.isEmpty ?? true {
            for item in duplicate.stockItems ?? [] { item.product = keeper }
        }
        for use in duplicate.recipeUses ?? [] { use.product = keeper }
        for item in duplicate.shoppingItems ?? [] { item.product = keeper }
    }
}
