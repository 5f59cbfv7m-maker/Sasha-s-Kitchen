#if os(iOS) && !targetEnvironment(macCatalyst)
import ActivityKit
import AppIntents
import Foundation
import SwiftData

/// «Иду в магазин»: список покупок на экране блокировки и в Dynamic Island,
/// отмечается прямо там, не разблокируя телефон.
nonisolated struct ShoppingTripAttributes: ActivityAttributes {
    struct ContentState: Codable, Hashable {
        /// Первые позиции списка — столько, сколько влезает на экран блокировки.
        var names: [String]
        var remaining: Int
    }
}

enum ShoppingTrip {
    static let visible = 4

    static var isActive: Bool { !Activity<ShoppingTripAttributes>.activities.isEmpty }

    static func state(in context: ModelContext) -> ShoppingTripAttributes.ContentState {
        let names = ShoppingListIntent.names(in: context)
        return .init(names: Array(names.prefix(visible)), remaining: names.count)
    }

    static func start(in context: ModelContext) {
        guard !isActive, ActivityAuthorizationInfo().areActivitiesEnabled else { return }
        let content = ActivityContent(state: state(in: context), staleDate: .now.addingTimeInterval(8 * 3600))
        do {
            _ = try Activity.request(attributes: ShoppingTripAttributes(), content: content)
        } catch {
            print("Не удалось начать поход в магазин: \(error)")
        }
    }

    /// Список поменялся — в приложении, в виджете или в самом Live Activity.
    /// Купили всё — поход окончен.
    static func update(in context: ModelContext) async {
        let state = state(in: context)
        for activity in Activity<ShoppingTripAttributes>.activities {
            if state.remaining == 0 {
                await activity.end(ActivityContent(state: state, staleDate: nil), dismissalPolicy: .after(.now + 60))
            } else {
                await activity.update(ActivityContent(state: state, staleDate: .now.addingTimeInterval(8 * 3600)))
            }
        }
    }

    static func end() async {
        for activity in Activity<ShoppingTripAttributes>.activities {
            await activity.end(nil, dismissalPolicy: .immediate)
        }
    }
}

/// Галочка в Live Activity: выполняется в приложении (система будит его в
/// фоне), поэтому пишет в ту же базу, что синхронизируется с iCloud.
struct TripBoughtIntent: LiveActivityIntent {
    static let title: LocalizedStringResource = "Отметить купленным"
    static let isDiscoverable = false

    @Parameter(title: "Продукт")
    var name: String

    init() {}

    init(name: String) {
        self.name = name
    }

    @MainActor
    func perform() async throws -> some IntentResult {
        let context = KitchenStore.shared.mainContext
        MarkBoughtIntent.markBought(name, in: context)
        await ShoppingTrip.update(in: context)
        return .result()
    }
}
#endif
