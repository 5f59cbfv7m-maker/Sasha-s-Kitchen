import Foundation
import SwiftData
import UserNotifications

/// Уведомления о сроках годности: накануне, в выбранное время — что завтра
/// испортится и что из этого можно приготовить. Включаются на каждом
/// устройстве отдельно (настройка не синхронизируется): иначе одно и то же
/// пришло бы разом на iPhone, iPad и Mac. На часы уведомления iPhone система
/// переносит сама.
enum ExpiryReminders {
    static let enabledKey = "expiryRemindersEnabled"
    /// Минуты от полуночи; по умолчанию 10:00.
    static let timeKey = "expiryRemindersTime"
    private static let prefix = "expiry-"
    /// Система держит не больше 64 запланированных уведомлений на приложение.
    private static let limit = 40

    static var isEnabled: Bool { UserDefaults.standard.bool(forKey: enabledKey) }
    static var minutes: Int { UserDefaults.standard.object(forKey: timeKey) as? Int ?? 600 }

    struct Notice: Equatable {
        let id: String
        let fireDate: Date
        let products: [String]
        let recipe: String?
    }

    /// Что и когда напомнить: по одному уведомлению на день, накануне дня,
    /// когда кончается срок. Прошедшее и пустые остатки не напоминаются.
    static func plan(stock: [StockItem], recipes: [Recipe], minutes: Int,
                     now: Date = .now, calendar: Calendar = .current) -> [Notice] {
        let snapshot = Kitchen.snapshot(of: stock)
        let byDay = Dictionary(grouping: stock.filter { !$0.isEmpty && $0.product != nil && $0.expiryDate != nil }) {
            calendar.startOfDay(for: $0.expiryDate!)
        }
        return byDay.keys.sorted().compactMap { day -> Notice? in
            guard let eve = calendar.date(byAdding: .day, value: -1, to: day),
                  let fire = calendar.date(byAdding: .minute, value: minutes, to: eve),
                  fire > now else { return nil }
            let names = byDay[day]!.map(\.name).sorted { $0.localizedStandardCompare($1) == .orderedAscending }
            return Notice(id: prefix + day.formatted(.iso8601.year().month().day()), fireDate: fire,
                          products: names, recipe: bestRecipe(using: Set(names), from: recipes, stock: snapshot))
        }
        .prefix(limit).map { $0 }
    }

    /// Готовый рецепт, который пускает в дело больше всего того, что испортится.
    static func bestRecipe(using products: Set<String>, from recipes: [Recipe], stock: StockSnapshot) -> String? {
        var best: (name: String, uses: Int)?
        for recipe in recipes {
            let uses = recipe.orderedIngredients.filter { products.contains($0.productName) }.count
            guard uses > 0, Kitchen.status(for: recipe, servings: recipe.baseServings, stock: stock).isReady else { continue }
            if let current = best, current.uses > uses || (current.uses == uses && current.name < recipe.name) { continue }
            best = (recipe.name, uses)
        }
        return best?.name
    }

    static func content(for notice: Notice) -> UNNotificationContent {
        let content = UNMutableNotificationContent()
        content.title = String(localized: "Завтра истекает срок годности")
        var body = ListFormatter.localizedString(byJoining: notice.products)
        if let recipe = notice.recipe {
            body += "\n" + String(localized: "Можно приготовить: \(recipe)")
        }
        content.body = body
        content.sound = .default
        content.threadIdentifier = "expiry"
        return content
    }

    /// Перепланировать всё заново: дешевле, чем искать, что изменилось.
    static func reschedule(in context: ModelContext) async {
        let center = UNUserNotificationCenter.current()
        let old = await center.pendingNotificationRequests().map(\.identifier).filter { $0.hasPrefix(prefix) }
        center.removePendingNotificationRequests(withIdentifiers: old)
        guard isEnabled else { return }
        let stock = Kitchen.allStock(in: context)
        let recipes = (try? context.fetch(FetchDescriptor<Recipe>())) ?? []
        for notice in plan(stock: stock, recipes: recipes, minutes: minutes) {
            let components = Calendar.current.dateComponents([.year, .month, .day, .hour, .minute], from: notice.fireDate)
            let request = UNNotificationRequest(identifier: notice.id, content: content(for: notice),
                                                trigger: UNCalendarNotificationTrigger(dateMatching: components, repeats: false))
            try? await center.add(request)
        }
    }

    /// Включение из настроек: сначала разрешение системы.
    static func enable() async -> Bool {
        let granted = (try? await UNUserNotificationCenter.current()
            .requestAuthorization(options: [.alert, .sound])) ?? false
        UserDefaults.standard.set(granted, forKey: enabledKey)
        return granted
    }
}
