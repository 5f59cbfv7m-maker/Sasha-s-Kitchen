import Foundation
import Testing
@testable import FridgeOracle

@Suite("КБЖУ")
struct NutritionTests {

    @Test("КБЖУ на 100 г масштабируется линейно")
    func per100Scaling() {
        let per100 = Nutrition(calories: 165, protein: 31, fat: 3.6, carbs: 0)
        let for300 = Nutrition.from(per100: per100, grams: 300)
        #expect(for300.calories == 495)
        #expect(for300.protein == 93)
        #expect(abs(for300.fat - 10.8) < 1e-9)
    }

    @Test("Сложение и суммирование последовательности совпадают")
    func summation() {
        let parts = [
            Nutrition(calories: 100, protein: 10, fat: 1, carbs: 5),
            Nutrition(calories: 50, protein: 2, fat: 3, carbs: 8),
        ]
        let sum = parts.total
        #expect(sum == parts[0] + parts[1])
        #expect(sum.calories == 150)
        #expect(sum.carbs == 13)
    }

    @Test("Штучные продукты считаются через вес одной штуки")
    func piecesUseGramsPerUnit() {
        let egg = Product(name: "Яйца", category: "Молочка/яйца", unit: "шт",
                          gramsPerUnit: 55, caloriesPer100: 155, proteinPer100: 13,
                          fatPer100: 11, carbsPer100: 1.1)
        // 3 шт = 165 г, ровно как в исходной таблице рецептов.
        #expect(abs(egg.nutrition(forAmount: 3).calories - 255.75) < 1e-9)
        #expect(egg.amountLabel(3) == "3 шт (165 г)")
    }

    @Test("Рецепт пересчитывается под число порций")
    func recipeScaling() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)

        let base = omelette.nutrition(for: 2)
        let double = omelette.nutrition(for: 4)
        #expect(abs(double.calories - base.calories * 2) < 1e-6)

        // На порцию величина от числа порций не зависит.
        let perTwo = omelette.nutritionPerServing(for: 2)
        let perFour = omelette.nutritionPerServing(for: 4)
        #expect(abs(perTwo.calories - perFour.calories) < 1e-6)
    }

    @Test("Граммовки в карточке рецепта растут пропорционально порциям")
    func requirementsScale() throws {
        let context = try makeContext(seeded: true)
        let buckwheat = try recipe("Гречка с курицей", in: context)

        let two = Kitchen.requirements(for: buckwheat, servings: 2)
        let three = Kitchen.requirements(for: buckwheat, servings: 3)
        #expect(two.count == three.count)
        for (base, scaled) in zip(two, three) {
            #expect(abs(scaled.amount - base.amount * 1.5) < 1e-9)
        }
    }
}
