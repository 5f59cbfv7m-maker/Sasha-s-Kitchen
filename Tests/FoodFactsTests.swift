import Foundation
import Testing
@testable import FridgeOracle

@Suite("Сканер: Open Food Facts и UPCitemdb")
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

    @Test("Ответ API v3 Open Food Facts разбирается так же")
    func parseV3() throws {
        let json = """
        {"status": "success", "result": {"id": "product_found"}, "product": {"product_name": "Nutella", "brands": "Nutella, Ferrero",
          "nutriments": {"energy-kcal_100g": 539, "proteins_100g": 6.3, "fat_100g": 30.9, "carbohydrates_100g": 57.5}}}
        """
        let info = try #require(FoodFacts.parse(Data(json.utf8), barcode: "3017620422003", language: "ru"))
        #expect(info.name == "Nutella" && info.brand == "Nutella" && info.source == .openFoodFacts)
        #expect(FoodFacts.parse(Data(#"{"status": "failure", "result": {"id": "product_not_found"}}"#.utf8),
                                barcode: "1", language: "en") == nil)
    }

    @Test("UPCitemdb: название, бренд, размер и картинка, без КБЖУ")
    func parseUPCitemdb() throws {
        let json = """
        {"code": "OK", "total": 1, "items": [{"ean": "0885909950805", "title": " Apple iPhone 6 ", "brand": "Apple",
          "size": "64 GB", "images": ["https://example.com/a.jpg"]}]}
        """
        let info = try #require(FoodFacts.parseUPCitemdb(Data(json.utf8), barcode: "885909950805"))
        #expect(info.name == "Apple iPhone 6" && info.brand == "Apple" && info.quantity == "64 GB")
        #expect(info.per100 == nil && info.source == .upcItemDB && info.imageURL != nil)
        #expect(FoodFacts.parseUPCitemdb(Data(#"{"code": "OK", "total": 0, "items": []}"#.utf8), barcode: "1") == nil)
        #expect(FoodFacts.parseUPCitemdb(Data(#"{"code": "TOO_FAST"}"#.utf8), barcode: "1") == nil)
    }
}
