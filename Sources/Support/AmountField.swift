import SwiftUI

/// Поле количества с подписью единицы. В американских единицах граммы
/// вводятся в унциях, миллилитры — в жидких унциях; в базу уходит метрика.
struct AmountField: View {
    @Binding var amount: Double
    let unit: String
    var width: CGFloat = 90
    var placeholder: LocalizedStringKey = "0"
    /// КБЖУ «на 100 г» — граммы и в американских единицах.
    var converts = true

    var body: some View {
        let input = converts ? Fmt.inputUnit(unit) : (label: Fmt.unit(unit), base: 1.0)
        HStack {
            TextField(placeholder, value: Binding(
                get: { amount / input.base },
                set: { amount = max(0, $0) * input.base }), format: Fmt.amountStyle)
                .keyboardType(.decimalPad)
                .multilineTextAlignment(.trailing)
                .monospacedDigit()
                .frame(maxWidth: width)
            Text(input.label)
                .foregroundStyle(.secondary)
                .frame(minWidth: 30, alignment: .leading)
        }
    }
}
