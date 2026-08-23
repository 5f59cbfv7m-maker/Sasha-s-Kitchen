import SwiftUI

struct StockCard: View {
    let item: StockItem

    private var tint: Color {
        if item.isEmpty { return .red }
        if item.isLow { return .orange }
        if item.isExpiringSoon { return .yellow }
        return .accentColor
    }

    private var statusText: String? {
        if item.isEmpty { return "закончилось" }
        if item.isLow { return "на исходе" }
        return nil
    }

    private var expiryNote: String? {
        guard !item.isEmpty, let days = item.daysUntilExpiry else { return nil }
        if days < 0 { return "срок вышел \(-days) дн. назад" }
        if days == 0 { return "годен сегодня" }
        if days <= 2 { return "годен ещё \(days) дн." }
        return nil
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: CategoryStyle.symbol(for: item.category))
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(.white)
                    .frame(width: 36, height: 36)
                    .background(CategoryStyle.color(for: item.category).gradient, in: .circle)

                VStack(alignment: .leading, spacing: 2) {
                    Text(item.name)
                        .font(.headline)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                    Text(item.category)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer(minLength: 0)

                if let statusText {
                    Text(statusText)
                        .font(.caption2.weight(.semibold))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(tint.opacity(0.18), in: .capsule)
                        .foregroundStyle(tint)
                }
            }

            VStack(alignment: .leading, spacing: 6) {
                HStack(alignment: .firstTextBaseline) {
                    Text(item.product?.amountLabel(item.currentAmount) ?? "—")
                        .font(.title3.weight(.semibold))
                        .monospacedDigit()
                    Spacer()
                    if item.initialAmount > 0 && !item.isEmpty {
                        Text("из \(Fmt.number(item.initialAmount))")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                            .monospacedDigit()
                    }
                }
                ProgressView(value: item.progress)
                    .tint(tint)
            }

            if let expiryNote {
                Label(expiryNote, systemImage: "clock")
                    .font(.caption)
                    .foregroundStyle(item.isExpiringSoon ? .orange : .secondary)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background.secondary, in: .rect(cornerRadius: 16))
        .overlay {
            RoundedRectangle(cornerRadius: 16)
                .strokeBorder(tint.opacity(item.isEmpty || item.isLow ? 0.5 : 0), lineWidth: 1.5)
        }
        .contentShape(.rect)
        .animation(.snappy, value: item.currentAmount)
    }
}
