import SwiftUI

/// Иконка и цвет категории — единственное место, где они задаются.
enum CategoryStyle {

    static func symbol(for category: String) -> String {
        switch category {
        case "Мясо": "fork.knife"
        case "Рыба": "fish.fill"
        case "Морепродукты": "water.waves"
        case "Молочка": "drop.fill"
        case "Молочка/яйца": "oval.fill"
        case "Крупы": "circle.grid.3x3.fill"
        case "Бакалея": "bag.fill"
        case "Овощи": "carrot.fill"
        case "Фрукты": "leaf.circle.fill"
        case "Зелень": "leaf.fill"
        case "Масла": "drop.triangle.fill"
        case "Специи": "sparkles"
        case "Соусы": "takeoutbag.and.cup.and.straw.fill"
        case "Хлеб": "birthday.cake.fill"
        default: "shippingbox.fill"
        }
    }

    static func color(for category: String) -> Color {
        switch category {
        case "Мясо": .red
        case "Рыба", "Морепродукты": .teal
        case "Молочка", "Молочка/яйца": .blue
        case "Крупы", "Бакалея": .brown
        case "Овощи": .orange
        case "Фрукты": .pink
        case "Зелень": .green
        case "Масла": .yellow
        case "Специи": .purple
        case "Соусы": .indigo
        case "Хлеб": Color(red: 0.72, green: 0.52, blue: 0.28)
        default: .gray
        }
    }

    /// Порядок разделов на экране холодильника: сначала то, что портится.
    static let order = ["Мясо", "Рыба", "Морепродукты", "Молочка/яйца", "Молочка",
                        "Овощи", "Зелень", "Фрукты", "Крупы", "Бакалея", "Хлеб",
                        "Соусы", "Масла", "Специи"]

    static func rank(_ category: String) -> Int {
        order.firstIndex(of: category) ?? order.count
    }
}
