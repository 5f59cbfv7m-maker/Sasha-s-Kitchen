import SwiftData
import SwiftUI

/// Журнал готовки — что уже приготовили и сколько это было в КБЖУ.
struct CookingLogView: View {
    @Environment(\.modelContext) private var context
    @Query(sort: \CookingLog.date, order: .reverse) private var logs: [CookingLog]

    private var byDay: [(day: Date, logs: [CookingLog])] {
        Dictionary(grouping: logs) { Calendar.current.startOfDay(for: $0.date) }
            .map { (day: $0.key, logs: $0.value.sorted { $0.date > $1.date }) }
            .sorted { $0.day > $1.day }
    }

    var body: some View {
        NavigationStack {
            Group {
                if logs.isEmpty {
                    EmptyStateView(symbol: "clock.arrow.circlepath",
                                   title: "Журнал пуст",
                                   message: "Здесь появятся блюда, которые вы приготовили: с фактическими граммовками и КБЖУ.")
                } else {
                    List {
                        ForEach(byDay, id: \.day) { group in
                            Section {
                                ForEach(group.logs) { log in
                                    row(log)
                                }
                                .onDelete { offsets in
                                    for index in offsets { context.delete(group.logs[index]) }
                                    try? context.save()
                                }
                            } header: {
                                HStack {
                                    Text(Fmt.date.string(from: group.day))
                                    Spacer()
                                    Text(Fmt.kcal(group.logs.map(\.calories).reduce(0, +)))
                                }
                            }
                        }
                    }
                }
            }
            .navigationTitle("Журнал")
        }
    }

    private func row(_ log: CookingLog) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(log.recipeName).font(.headline)
                Spacer()
                Text(log.date, style: .time)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Text("\(log.servings) порц. · \(Fmt.kcal(log.nutritionPerServing.calories)) на порцию")
                .font(.caption)
                .foregroundStyle(.secondary)
            NutritionRow(nutrition: log.nutrition)
            if !log.actualAmountsUsed.isEmpty {
                Text(log.actualAmountsUsed
                    .sorted { $0.key < $1.key }
                    .map { "\($0.key) \(Fmt.number($0.value))" }
                    .joined(separator: " · "))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 4)
    }
}
