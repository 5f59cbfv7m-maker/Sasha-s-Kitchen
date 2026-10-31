import AppIntents
import SwiftData
import SwiftUI
import WidgetKit

@main
struct KitchenWidgetBundle: WidgetBundle {
    var body: some Widget {
        ShoppingListWidget()
        ExpiringWidget()
        ShoppingTripLiveActivity()
    }
}

// MARK: Данные

struct KitchenEntry: TimelineEntry {
    struct Row: Hashable {
        let name: String
        let detail: String
    }

    let date: Date
    let toBuy: [Row]
    let expiring: [Row]

    static let sample = KitchenEntry(date: .now,
        toBuy: [Row(name: "Молоко", detail: "1 л"), Row(name: "Хлеб", detail: ""), Row(name: "Яйца", detail: "10 шт")],
        expiring: [Row(name: "Творог", detail: "годен сегодня")])

    /// Тот же список, что на вкладке «Для покупки», и сроки — как на карточках.
    @MainActor
    static func load() -> KitchenEntry {
        let context = KitchenStore.shared.mainContext
        let listed = ((try? context.fetch(FetchDescriptor<ShoppingItem>(sortBy: [SortDescriptor(\.createdAt)]))) ?? [])
            .filter { !$0.isBought }
            .map { Row(name: $0.name, detail: $0.amount > 0 ? $0.product?.amountLabel($0.amount) ?? "" : "") }
        let fridge = Kitchen.shoppingEntries(in: context)
            .filter { entry in !listed.contains { $0.name.sameName(as: entry.item.name) } }
            .map { Row(name: $0.item.name, detail: $0.item.product?.amountLabel($0.suggested) ?? "") }
        let expiring = Kitchen.allStock(in: context)
            .filter { !$0.isEmpty && $0.isExpiringSoon }
            .sorted { ($0.daysUntilExpiry ?? 0) < ($1.daysUntilExpiry ?? 0) }
            .map { Row(name: $0.name, detail: Self.expiryText($0.daysUntilExpiry ?? 0)) }
        return KitchenEntry(date: .now, toBuy: listed + fridge, expiring: expiring)
    }

    static func expiryText(_ days: Int) -> String {
        days <= 0 ? String(localized: "годен сегодня") : String(localized: "годен ещё \(days) дн.")
    }
}

struct KitchenProvider: TimelineProvider {
    func placeholder(in context: Context) -> KitchenEntry { .sample }

    func getSnapshot(in context: Context, completion: @escaping (KitchenEntry) -> Void) {
        Task { @MainActor in completion(context.isPreview ? .sample : KitchenEntry.load()) }
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<KitchenEntry>) -> Void) {
        // Приложение перезагружает виджеты при каждом изменении; раз в час —
        // на случай, если пришло из iCloud, пока оно спало, и ради сроков.
        Task { @MainActor in completion(Timeline(entries: [KitchenEntry.load()], policy: .after(.now + 3600))) }
    }
}

// MARK: Список покупок

struct ShoppingListWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "ShoppingList", provider: KitchenProvider()) { entry in
            ShoppingListView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Список покупок")
        .description("Что купить — отмечайте прямо на виджете.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge,
                            .accessoryRectangular, .accessoryInline, .accessoryCircular])
    }
}

private struct ShoppingListView: View {
    @Environment(\.widgetFamily) private var family
    let entry: KitchenEntry

    private var rows: Int {
        switch family {
        case .systemSmall: 3
        case .systemMedium: 3
        case .systemLarge: 8
        default: 2
        }
    }

    var body: some View {
        switch family {
        case .accessoryInline:
            Label("Купить: \(entry.toBuy.count)", systemImage: "cart")
        case .accessoryCircular:
            Gauge(value: 0) { Image(systemName: "cart") } currentValueLabel: { Text("\(entry.toBuy.count)") }
                .gaugeStyle(.accessoryCircularCapacity)
        case .accessoryRectangular:
            VStack(alignment: .leading, spacing: 1) {
                Label("Купить: \(entry.toBuy.count)", systemImage: "cart").font(.headline)
                ForEach(entry.toBuy.prefix(rows), id: \.self) { Text($0.name).lineLimit(1) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        case .systemSmall:
            VStack(alignment: .leading, spacing: 6) {
                header
                ForEach(entry.toBuy.prefix(rows), id: \.self) { Text($0.name).font(.subheadline).lineLimit(1) }
                Spacer(minLength: 0)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        default:
            VStack(alignment: .leading, spacing: 8) {
                header
                if entry.toBuy.isEmpty {
                    Text("Покупать нечего").font(.subheadline).foregroundStyle(.secondary)
                }
                ForEach(entry.toBuy.prefix(rows), id: \.self) { row in
                    // Галочка выполняет «Купил» в базе, не открывая приложение.
                    Button(intent: MarkBoughtIntent(name: row.name)) {
                        HStack(spacing: 8) {
                            Image(systemName: "circle").foregroundStyle(.green)
                            Text(row.name).lineLimit(1)
                            Spacer(minLength: 4)
                            Text(row.detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                }
                Spacer(minLength: 0)
            }
        }
    }

    private var header: some View {
        HStack {
            Label("Для покупки", systemImage: "cart.fill")
                .font(.headline)
                .foregroundStyle(.green)
            Spacer()
            Text("\(entry.toBuy.count)").font(.headline.monospacedDigit())
        }
    }
}

// MARK: Сроки годности

struct ExpiringWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "Expiring", provider: KitchenProvider()) { entry in
            ExpiringView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Сроки годности")
        .description("Что испортится в ближайшие дни.")
        .supportedFamilies([.systemSmall, .systemMedium, .accessoryRectangular])
    }
}

private struct ExpiringView: View {
    @Environment(\.widgetFamily) private var family
    let entry: KitchenEntry

    var body: some View {
        VStack(alignment: .leading, spacing: family == .accessoryRectangular ? 1 : 6) {
            Label("Скоро испортится", systemImage: "clock.badge.exclamationmark")
                .font(.headline)
                .foregroundStyle(family == .accessoryRectangular ? .primary : Color.orange)
                .lineLimit(1)
            if entry.expiring.isEmpty {
                Text("В ближайшие дни ничего не испортится.").font(.caption).foregroundStyle(.secondary)
            }
            ForEach(entry.expiring.prefix(family == .systemMedium ? 4 : 3), id: \.self) { row in
                HStack {
                    Text(row.name).lineLimit(1)
                    Spacer(minLength: 4)
                    if family != .accessoryRectangular {
                        Text(row.detail).font(.caption).foregroundStyle(.orange).lineLimit(1)
                    }
                }
                .font(family == .accessoryRectangular ? .body : .subheadline)
            }
            Spacer(minLength: 0)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: Поход в магазин

struct ShoppingTripLiveActivity: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: ShoppingTripAttributes.self) { context in
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Label("Поход в магазин", systemImage: "cart.fill").font(.headline).foregroundStyle(.green)
                    Spacer()
                    Text("Осталось: \(context.state.remaining)").font(.subheadline.monospacedDigit())
                }
                ForEach(context.state.names, id: \.self) { name in
                    Button(intent: TripBoughtIntent(name: name)) {
                        HStack(spacing: 8) {
                            Image(systemName: "circle").foregroundStyle(.green)
                            Text(name).lineLimit(1)
                            Spacer()
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding()
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Label("\(context.state.remaining)", systemImage: "cart.fill").foregroundStyle(.green)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    VStack(alignment: .leading, spacing: 6) {
                        ForEach(context.state.names.prefix(2), id: \.self) { name in
                            Button(intent: TripBoughtIntent(name: name)) {
                                Label(name, systemImage: "circle")
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            } compactLeading: {
                Image(systemName: "cart.fill").foregroundStyle(.green)
            } compactTrailing: {
                Text("\(context.state.remaining)").monospacedDigit()
            } minimal: {
                Text("\(context.state.remaining)").monospacedDigit()
            }
        }
    }
}
