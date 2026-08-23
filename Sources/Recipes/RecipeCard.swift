import SwiftUI

struct RecipeCard: View {
    let recipe: Recipe
    let status: RecipeAvailability.Status
    let servings: Int

    private var perServing: Nutrition { recipe.nutritionPerServing(for: servings) }

    private var missing: [Shortage] {
        if case .missing(let shortages) = status { return shortages }
        return []
    }

    /// Если фото нет, заглушку рисуем по «главному» ингредиенту — тому, что
    /// весит больше всех, не считая специй и масла. Мясное блюдо получается
    /// красным, рыбное — бирюзовым, овощное — оранжевым.
    private var heroCategory: String {
        let background: Set<String> = ["Специи", "Масла", "Соусы", "Зелень"]
        let weighted = recipe.orderedIngredients.compactMap { ingredient -> (String, Double)? in
            guard let product = ingredient.product else { return nil }
            let grams = ingredient.amountPerBaseServing * product.gramsPerUnit
            return (product.category, background.contains(product.category) ? grams * 0.01 : grams)
        }
        return weighted.max { $0.1 < $1.1 }?.0 ?? "Прочее"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ZStack(alignment: .bottomLeading) {
                if let data = recipe.photoData, let image = UIImage(data: data) {
                    Image(uiImage: image)
                        .resizable()
                        .scaledToFill()
                } else {
                    let tint = CategoryStyle.color(for: heroCategory)
                    LinearGradient(colors: [tint.mix(with: .white, by: 0.30),
                                            tint.mix(with: .black, by: 0.18)],
                                   startPoint: .topLeading, endPoint: .bottomTrailing)
                        .overlay {
                            Image(systemName: CategoryStyle.symbol(for: heroCategory))
                                .font(.system(size: 34, weight: .semibold))
                                .foregroundStyle(.white.opacity(0.55))
                        }
                }
            }
            .frame(height: 118)
            .frame(maxWidth: .infinity)
            .clipped()
            .overlay(alignment: .topTrailing) {
                if recipe.isCustom {
                    Label("Мой", systemImage: "person.fill")
                        .font(.caption2.weight(.semibold))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(.ultraThinMaterial, in: .capsule)
                        .padding(8)
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Text(recipe.name)
                    .font(.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)

                HStack(spacing: 10) {
                    Label(Fmt.minutes(recipe.cookingTimeMinutes), systemImage: "clock")
                    Label(Fmt.kcal(perServing.calories), systemImage: "flame")
                }
                .font(.caption)
                .foregroundStyle(.secondary)

                availabilityBadge
            }
            .padding(12)
        }
        .background(.background.secondary, in: .rect(cornerRadius: 16))
        .clipShape(.rect(cornerRadius: 16))
        .contentShape(.rect)
    }

    @ViewBuilder
    private var availabilityBadge: some View {
        if status.isReady {
            Label("Все ингредиенты есть", systemImage: "checkmark.circle.fill")
                .font(.caption.weight(.medium))
                .foregroundStyle(.green)
                .lineLimit(1)
        } else {
            Label("Не хватает: \(missing.map(\.productName).joined(separator: ", "))",
                  systemImage: "exclamationmark.triangle.fill")
                .font(.caption)
                .foregroundStyle(.orange)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}
