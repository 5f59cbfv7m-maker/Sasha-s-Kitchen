import SwiftData
import SwiftUI
import WidgetKit

/// Значок на циферблате: сколько купить и что первым.
@main
struct WatchShoppingWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "WatchShoppingList", provider: WatchProvider()) { entry in
            WatchShoppingView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Список покупок")
        .description("Что купить — на циферблате.")
        .supportedFamilies([.accessoryCircular, .accessoryRectangular, .accessoryInline, .accessoryCorner])
    }
}

struct WatchEntry: TimelineEntry {
    let date: Date
    let names: [String]

    @MainActor
    static func load() -> WatchEntry {
        WatchEntry(date: .now, names: ShoppingListIntent.names(in: KitchenStore.shared.mainContext))
    }
}

struct WatchProvider: TimelineProvider {
    func placeholder(in context: Context) -> WatchEntry { WatchEntry(date: .now, names: ["Молоко", "Хлеб"]) }

    func getSnapshot(in context: Context, completion: @escaping (WatchEntry) -> Void) {
        Task { @MainActor in completion(WatchEntry.load()) }
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<WatchEntry>) -> Void) {
        Task { @MainActor in completion(Timeline(entries: [WatchEntry.load()], policy: .after(.now + 3600))) }
    }
}

private struct WatchShoppingView: View {
    @Environment(\.widgetFamily) private var family
    let entry: WatchEntry

    var body: some View {
        switch family {
        case .accessoryInline:
            Label("Купить: \(entry.names.count)", systemImage: "cart")
        case .accessoryRectangular:
            VStack(alignment: .leading, spacing: 1) {
                Label("Купить: \(entry.names.count)", systemImage: "cart").font(.headline)
                ForEach(entry.names.prefix(2), id: \.self) { Text($0).lineLimit(1) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        case .accessoryCorner:
            Image(systemName: "cart")
                .widgetLabel("\(entry.names.count)")
        default:
            ZStack {
                AccessoryWidgetBackground()
                VStack(spacing: 0) {
                    Image(systemName: "cart").font(.caption)
                    Text("\(entry.names.count)").font(.headline.monospacedDigit())
                }
            }
        }
    }
}
