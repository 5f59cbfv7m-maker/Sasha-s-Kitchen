import Foundation

/// Клиент магазина рецептов (`server/`, контракт — `server/docs/API.md`).
/// Пока адреса нет, магазина в приложении нет вовсе: вкладка прячется.
/// Адрес — ключ `SKStoreBaseURL` в Info.plist (`$(SK_STORE_URL)` при сборке)
/// или `-storeURL http://127.0.0.1:8080` в аргументах запуска для проверки.
enum StoreAPI {
    static var baseURL: URL? {
        let raw = UserDefaults.standard.string(forKey: "storeURL")
            ?? Bundle.main.object(forInfoDictionaryKey: "SKStoreBaseURL") as? String ?? ""
        return raw.isEmpty ? nil : URL(string: raw)
    }

    static var isConfigured: Bool { baseURL != nil }

    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    private struct Envelope: Decodable {
        struct Body: Decodable { let message: String }
        let error: Body
    }

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }()

    static func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        try await send("GET", path, query: query)
    }

    static func send<T: Decodable>(_ method: String, _ path: String, query: [URLQueryItem] = []) async throws -> T {
        let data = try await request(method, path, query: query)
        return try decoder.decode(T.self, from: data)
    }

    /// Ответ без тела (204) — например, засчитать просмотр.
    static func fire(_ method: String, _ path: String) async {
        _ = try? await request(method, path)
    }

    private static func request(_ method: String, _ path: String, query: [URLQueryItem] = []) async throws -> Data {
        guard let base = baseURL,
              var components = URLComponents(url: base.appending(path: path), resolvingAgainstBaseURL: false)
        else { throw Failure(message: String(localized: "Магазин недоступен")) }
        if !query.isEmpty { components.queryItems = query }
        var request = URLRequest(url: components.url!, timeoutInterval: 20)
        request.httpMethod = method
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            // Сервер объясняет ошибку сам, по-русски; иначе — общий текст.
            let message = (try? decoder.decode(Envelope.self, from: data))?.error.message
            throw Failure(message: message ?? String(localized: "Магазин недоступен"))
        }
        return data
    }
}

// MARK: Ответы сервера

struct StoreMedia: Decodable, Hashable {
    let kind: String
    let url: URL?
    let posterUrl: URL?
    let width: Int?
    let height: Int?

    /// Что показать картинкой: у видео — кадр-постер.
    var imageURL: URL? { kind == "video" ? posterUrl ?? url : url }
}

struct StoreAuthor: Decodable, Hashable {
    let handle: String
    let name: String
}

struct StoreNutrition: Decodable, Hashable {
    let kcal, protein, fat, carbs: Double
}

struct StoreCard: Decodable, Hashable, Identifiable {
    let id: String
    let title: String
    let summary: String?
    let cookTimeMinutes: Int
    let baseServings: Int
    let nutritionPerServing: StoreNutrition
    let author: StoreAuthor
    let media: StoreMedia?
}

struct StoreFront: Decodable {
    struct Shelf: Decodable, Identifiable {
        let slug: String
        let title: String
        let items: [StoreCard]
        var id: String { slug }
    }
    let shelves: [Shelf]
}

struct StoreRecipeDetail: Decodable {
    struct Step: Decodable, Hashable { let order: Int; let text: String }
    struct Ingredient: Decodable, Hashable {
        let name: String
        let amount: Double
        let unit: String
        let optional: Bool
    }
    let id: String
    let title: String
    let summary: String?
    let description: String?
    let cookTimeMinutes: Int
    let prepTimeMinutes: Int
    let baseServings: Int
    let nutritionPerServing: StoreNutrition
    let author: StoreAuthor
    let media: StoreMedia?
    let steps: [Step]?
    let ingredients: [Ingredient]?
    let canImport: Bool
}

/// Самодостаточный рецепт для импорта: продукты описаны целиком.
struct StoreBundle: Decodable {
    struct Recipe: Decodable {
        let sourceId: String
        let title: String
        let steps: [StoreRecipeDetail.Step]
        let cookTimeMinutes: Int
        let prepTimeMinutes: Int
        let baseServings: Int
        let author: StoreAuthor
        let photoUrl: URL?
    }
    struct Product: Decodable {
        let key: String
        let name: String
        let category: String?
        let unit: String
        let gramsPerUnit: Double
        let nutritionPer100: StoreNutrition
        let shelfLifeDays: Int?
        let lowStockThreshold: Double
    }
    struct Ingredient: Decodable {
        let productKey: String
        let amountPerBaseServing: Double
        let unit: String
        let position: Int
    }
    let schemaVersion: Int
    let recipe: Recipe
    let products: [Product]
    let ingredients: [Ingredient]
}

struct StorePost: Decodable, Hashable, Identifiable {
    let id: String
    let title: String
    let likeCount: Int
    let viewCount: Int
    let author: StoreAuthor
    let media: StoreMedia?
}

struct StorePage<Item: Decodable>: Decodable {
    let items: [Item]
    let nextCursor: String?
    let hasMore: Bool
}
