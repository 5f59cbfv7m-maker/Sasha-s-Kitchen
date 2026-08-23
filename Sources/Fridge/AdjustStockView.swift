import SwiftData
import SwiftUI

/// Правка остатка вручную: докупили, доели, пересчитали.
struct AdjustStockView: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.modelContext) private var context

    let item: StockItem
    @State private var amount: Double = 0
    @State private var expiryEnabled = false
    @State private var expiry = Date.now

    private var unit: String { item.unit }

    var body: some View {
        NavigationStack {
            Form {
                Section("Остаток") {
                    HStack {
                        TextField("Количество", value: $amount,
                                  format: Fmt.amountStyle)
                            .keyboardType(.decimalPad)
                            .multilineTextAlignment(.trailing)
                            .monospacedDigit()
                        Text(unit).foregroundStyle(.secondary)
                    }
                    HStack(spacing: 8) {
                        ForEach(quickSteps, id: \.self) { step in
                            Button("+\(Fmt.number(step))") {
                                amount += step
                                Feedback.shared.play(.added)
                            }
                            .buttonStyle(.bordered)
                        }
                        Button("Обнулить") {
                            amount = 0
                            Feedback.shared.play(.removed)
                        }
                        .buttonStyle(.bordered)
                        .tint(.red)
                    }
                    .buttonStyle(.bordered)
                }

                Section("Срок годности") {
                    Toggle("Указать срок", isOn: $expiryEnabled.animation())
                    if expiryEnabled {
                        DatePicker("Годен до", selection: $expiry, displayedComponents: .date)
                    }
                }

                if let product = item.product {
                    Section("КБЖУ в этом остатке") {
                        NutritionRow(nutrition: product.nutrition(forAmount: amount))
                            .listRowInsets(EdgeInsets(top: 8, leading: 12, bottom: 8, trailing: 12))
                    }
                }
            }
            .navigationTitle(item.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Сохранить") { save() }
                }
            }
        }
        .onAppear {
            amount = item.currentAmount
            expiryEnabled = item.expiryDate != nil
            expiry = item.expiryDate ?? Date.now.addingTimeInterval(3 * 86_400)
        }
    }

    /// Шаги подгоняются под единицу: штуки прибавляем по одной, граммы — сотнями.
    private var quickSteps: [Double] {
        unit == "шт" ? [1, 5, 10] : [50, 100, 500]
    }

    private func save() {
        Kitchen.setAmount(item, to: amount)
        item.expiryDate = expiryEnabled ? expiry : nil
        try? context.save()
        Feedback.shared.play(.added)
        dismiss()
    }
}
