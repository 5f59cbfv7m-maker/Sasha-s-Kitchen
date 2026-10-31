import Foundation
import Testing
@testable import FridgeOracle

@Suite("Сканер: Open Food Facts")
struct FoodFactsTests {

    @Test("В адрес запроса попадают только 8–14 цифр")
    func barcodeValidation() {
        #expect(FoodFacts.normalized("4607001771470") == "4607001771470")
        #expect(FoodFacts.normalized(" 40084107 ") == "40084107")
        #expect(FoodFacts.normalized("1234567") == nil)
        #expect(FoodFacts.normalized("12345678/../x") == nil)
        #expect(FoodFacts.normalized("4607001771470?a=b") == nil)
    }

    @Test("Ответ разбирается: имя на языке устройства, первый бренд, КБЖУ на 100 г")
    func parse() throws {
        let json = """
        {"status": 1, "product": {
          "product_name": "Milk", "product_name_ru": "Молоко", "brands": "Простоквашино, Danone",
          "quantity": "930 ml", "image_front_small_url": "https://images.openfoodfacts.org/x.jpg",
          "nutriments": {"energy-kcal_100g": 52, "proteins_100g": 2.8, "fat_100g": 2.5, "carbohydrates_100g": 4.7}}}
        """
        let info = try #require(FoodFacts.parse(Data(json.utf8), barcode: "4607001771470", language: "ru"))
        #expect(info.name == "Молоко")
        #expect(info.brand == "Простоквашино")
        #expect(info.quantity == "930 ml")
        #expect(info.per100 == Nutrition(calories: 52, protein: 2.8, fat: 2.5, carbs: 4.7))

        let english = try #require(FoodFacts.parse(Data(json.utf8), barcode: "1", language: "de"))
        #expect(english.name == "Milk")
    }

    @Test("Не найдено или без имени — nil, без КБЖУ — продукт без пищевой ценности")
    func parseEdgeCases() throws {
        #expect(FoodFacts.parse(Data(#"{"status": 0}"#.utf8), barcode: "1", language: "en") == nil)
        #expect(FoodFacts.parse(Data(#"{"status": 1, "product": {"product_name": " "}}"#.utf8),
                                barcode: "1", language: "en") == nil)
        let bare = try #require(FoodFacts.parse(Data(#"{"status": 1, "product": {"product_name": "Tea"}}"#.utf8),
                                                barcode: "1", language: "en"))
        #expect(bare.per100 == nil)
        #expect(bare.brand == nil)
    }
}
