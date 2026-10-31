import CryptoKit
import Foundation

/// Фото стартовых рецептов: открытые снимки с Wikimedia Commons (авторы и
/// лицензии — `recipe-photo-credits.json`, экран «Фото рецептов» в настройках).
/// Лежат в приложении для iPhone, iPad и Mac; на часах их нет — там фото не нужны.
enum SeedPhotos {
    /// Имя файла — от ключа рецепта (русского названия), а не от перевода.
    static func fileName(for key: String) -> String {
        "recipe-" + SHA256.hash(data: Data(key.utf8)).prefix(6).map { String(format: "%02x", $0) }.joined()
    }

    static func data(for key: String) -> Data? {
        Bundle.main.url(forResource: fileName(for: key), withExtension: "heic").flatMap { try? Data(contentsOf: $0) }
    }
}
