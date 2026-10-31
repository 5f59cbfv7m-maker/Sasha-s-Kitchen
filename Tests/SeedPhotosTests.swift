import Foundation
import Testing
@testable import FridgeOracle

@Suite("Фото стартовых рецептов")
@MainActor
struct SeedPhotosTests {

    @Test("У каждого рецепта всех наборов есть фото, и у каждого фото — автор и лицензия")
    func everyRecipeHasPhotoAndCredit() throws {
        let keys = SeedSet.allCases.flatMap { $0.recipes.map(\.name) }
        for key in keys {
            #expect(SeedPhotos.data(for: key) != nil, "нет фото: \(key)")
        }
        let url = try #require(Bundle.main.url(forResource: "recipe-photo-credits", withExtension: "json"))
        let credits = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [[String: String]] ?? []
        #expect(Set(credits.compactMap { $0["key"] }) == Set(keys))
        #expect(credits.allSatisfy { !($0["author"] ?? "").isEmpty && !($0["license"] ?? "").isEmpty })
    }

    @Test("Стартовый набор кладёт фото в рецепты")
    func seedLoadsPhotos() throws {
        let context = try makeContext(seeded: true)
        let omelette = try recipe("Омлет классический", in: context)
        #expect(omelette.photoData != nil)
    }
}
